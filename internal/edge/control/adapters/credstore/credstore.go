// SPDX-License-Identifier: MIT

// Package credstore keeps the control page's passphrase hash in the edge's data volume and serves
// it on the edge admin socket. It runs inside the edge: the control service has a read-only disk
// and no volume of its own. The file is mode 0600, so only the edge user reads it.
//
//	GET    /v1/control/credential  {"hash":"$argon2id$..."}, or 404 when none is set
//	PUT    /v1/control/credential  {"hash":"$argon2id$..."} -> 201, or 409 when one is set
//	DELETE /v1/control/credential  204 (the reset; idempotent)
//
// Errors are RFC 9457 problem documents typed urn:agentsone:problem:<slug>.
package credstore

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// FileName is the hash file in the edge data folder.
const FileName = "control-credential"

// maxHash bounds a stored hash.
const maxHash = 512

// Errors of the store.
var (
	ErrNone    = errors.New("no control passphrase is set")
	ErrExists  = errors.New("a control passphrase is already set")
	ErrInvalid = errors.New("this is not an argon2id hash")
)

// Store is the hash file.
type Store struct {
	path string
	mu   sync.Mutex
}

// New returns the store in dir.
func New(dir string) *Store { return &Store{path: filepath.Join(dir, FileName)} }

// Path is the hash file.
func (s *Store) Path() string { return s.path }

func valid(hash string) bool {
	return strings.HasPrefix(hash, "$argon2id$") && len(hash) <= maxHash && !strings.ContainsAny(hash, " \t\r\n")
}

// Get returns the hash, or ErrNone.
func (s *Store) Get() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := os.ReadFile(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", ErrNone
	}
	if err != nil {
		return "", fmt.Errorf("read control credential: %w", err)
	}
	if hash := string(data); valid(hash) {
		return hash, nil
	}
	return "", fmt.Errorf("read control credential %s: %w", s.path, ErrInvalid)
}

// Create stores the first hash. It writes a temporary file and links it into place, so the hash
// file is either whole or absent, and a second create fails with ErrExists.
func (s *Store) Create(hash string) error {
	if !valid(hash) {
		return ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("control credential folder: %w", err)
	}
	tmp, err := os.CreateTemp(dir, "."+FileName+"-*")
	if err != nil {
		return fmt.Errorf("control credential: %w", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }() //nolint:errcheck // temp file cleanup; a leftover dot file in the folder is never read
	if _, err := tmp.WriteString(hash); err != nil {
		return errors.Join(fmt.Errorf("write control credential: %w", err), tmp.Close())
	}
	if err := tmp.Sync(); err != nil {
		return errors.Join(fmt.Errorf("write control credential: %w", err), tmp.Close())
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write control credential: %w", err)
	}
	if err := os.Link(tmp.Name(), s.path); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return ErrExists
		}
		return fmt.Errorf("store control credential: %w", err)
	}
	return nil
}

// Delete removes the hash. Removing nothing is not an error.
func (s *Store) Delete() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.Remove(s.path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove control credential: %w", err)
	}
	return nil
}

type body struct {
	Hash string `json:"hash"`
}

// Register adds the credential routes to the admin mux.
func Register(mux *http.ServeMux, s *Store, log *slog.Logger) {
	internal := func(w http.ResponseWriter, err error) {
		log.Error("control credential", "err", err)
		writeProblem(w, http.StatusInternalServerError, "internal", "The edge could not do this. Its log has the reason.")
	}
	mux.HandleFunc("GET /v1/control/credential", func(w http.ResponseWriter, _ *http.Request) {
		hash, err := s.Get()
		switch {
		case errors.Is(err, ErrNone):
			writeProblem(w, http.StatusNotFound, "not-found", "No control passphrase is set.")
		case err != nil:
			internal(w, err)
		default:
			writeJSON(w, http.StatusOK, body{Hash: hash})
		}
	})
	mux.HandleFunc("PUT /v1/control/credential", func(w http.ResponseWriter, r *http.Request) {
		var in body
		if err := decode(r, &in); err != nil {
			writeProblem(w, http.StatusBadRequest, "validation", err.Error())
			return
		}
		switch err := s.Create(in.Hash); {
		case errors.Is(err, ErrInvalid):
			writeProblem(w, http.StatusBadRequest, "validation", "The hash must be an argon2id hash.")
		case errors.Is(err, ErrExists):
			writeProblem(w, http.StatusConflict, "conflict", "A control passphrase is already set. Reset it first.")
		case err != nil:
			internal(w, err)
		default:
			log.Info("control passphrase set")
			w.WriteHeader(http.StatusCreated)
		}
	})
	mux.HandleFunc("DELETE /v1/control/credential", func(w http.ResponseWriter, _ *http.Request) {
		if err := s.Delete(); err != nil {
			internal(w, err)
			return
		}
		log.Info("control passphrase reset")
		w.WriteHeader(http.StatusNoContent)
	})
}

const maxBody = 4 << 10

func decode(r *http.Request, v any) error {
	data, err := io.ReadAll(io.LimitReader(r.Body, maxBody+1))
	if err != nil {
		return errors.New("the request body could not be read")
	}
	if len(data) > maxBody {
		return errors.New("the request body is too large")
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return errors.New("the request body is not valid JSON of the expected shape")
	}
	return nil
}

type problem struct {
	Type   string `json:"type"`
	Title  string `json:"title"`
	Status int    `json:"status"`
	Detail string `json:"detail,omitempty"`
}

func writeProblem(w http.ResponseWriter, status int, slug, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(problem{ //nolint:errcheck // the status is sent; a failed body write means the client left
		Type: "urn:agentsone:problem:" + slug, Title: http.StatusText(status), Status: status, Detail: detail,
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v) //nolint:errcheck // the status is sent; a failed body write means the client left
}
