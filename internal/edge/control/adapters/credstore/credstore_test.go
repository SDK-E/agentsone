// SPDX-License-Identifier: MIT

package credstore_test

import (
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SDK-E/agentsone/internal/edge/control/adapters/credstore"
)

const hash = "$argon2id$v=19$m=64,t=1,p=1$c2FsdHNhbHRzYWx0$aGFzaGhhc2hoYXNoaGFzaA"

func TestStore_GetWithNothingStoredSaysNone(t *testing.T) {
	t.Parallel()
	s := credstore.New(t.TempDir())
	if _, err := s.Get(); !errors.Is(err, credstore.ErrNone) {
		t.Fatalf("Get() error = %v, want %v", err, credstore.ErrNone)
	}
}

func TestStore_CreateKeepsTheHashInAFileOnlyTheEdgeCanRead(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s := credstore.New(dir)

	if err := s.Create(hash); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	got, err := s.Get()
	if err != nil || got != hash {
		t.Fatalf("Get() = %q, %v", got, err)
	}
	info, err := os.Stat(filepath.Join(dir, credstore.FileName))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("file mode = %v, want 0600", info.Mode().Perm())
	}
	if got, _ := credstore.New(dir).Get(); got != hash {
		t.Fatal("a new store on the same folder does not see the hash")
	}
}

func TestStore_CreateRefusesASecondHash(t *testing.T) {
	t.Parallel()
	s := credstore.New(t.TempDir())
	_ = s.Create(hash)

	if err := s.Create(strings.Replace(hash, "aGFz", "b3Ro", 1)); !errors.Is(err, credstore.ErrExists) {
		t.Fatalf("second Create() error = %v, want %v", err, credstore.ErrExists)
	}
	if got, _ := s.Get(); got != hash {
		t.Fatal("the second create changed the hash")
	}
}

func TestStore_CreateRefusesSomethingThatIsNotAnArgon2idHash(t *testing.T) {
	t.Parallel()
	s := credstore.New(t.TempDir())
	for _, bad := range []string{"", "plain text", "$argon2i$v=19$x", hash + "\n", "$argon2id$" + strings.Repeat("a", 600)} {
		if err := s.Create(bad); !errors.Is(err, credstore.ErrInvalid) {
			t.Errorf("Create(%q) error = %v, want %v", bad, err, credstore.ErrInvalid)
		}
	}
}

func TestStore_DeleteClearsTheHashAndIsIdempotent(t *testing.T) {
	t.Parallel()
	s := credstore.New(t.TempDir())
	_ = s.Create(hash)

	if err := s.Delete(); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if err := s.Delete(); err != nil {
		t.Fatalf("second Delete() error = %v", err)
	}
	if _, err := s.Get(); !errors.Is(err, credstore.ErrNone) {
		t.Fatalf("Get() after delete error = %v", err)
	}
}

func TestStore_GetRefusesAFileThatIsNotAHash(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, credstore.FileName), []byte("garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := credstore.New(dir).Get(); err == nil || errors.Is(err, credstore.ErrNone) {
		t.Fatalf("Get() of a bad file error = %v, want a read error", err)
	}
}

func TestStore_CreateFailsInAFolderThatCannotBeWritten(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	s := credstore.New(filepath.Join(root, "missing", "deeper"))
	if err := os.WriteFile(filepath.Join(root, "missing"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.Create(hash); err == nil || errors.Is(err, fs.ErrExist) {
		t.Fatalf("Create() in a folder under a file: error = %v", err)
	}
}

func serve(t *testing.T, s *credstore.Store, method, body string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	credstore.Register(mux, s, slog.New(slog.DiscardHandler))
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), method, "/v1/control/credential", r))
	return rec
}

func problemType(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("Content-Type = %q, want application/problem+json", ct)
	}
	var p struct {
		Type   string `json:"type"`
		Status int    `json:"status"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	if p.Status != rec.Code {
		t.Fatalf("problem status = %d, want %d", p.Status, rec.Code)
	}
	return p.Type
}

func TestRoutes_GetWithNothingStoredIsNotFound(t *testing.T) {
	t.Parallel()
	rec := serve(t, credstore.New(t.TempDir()), http.MethodGet, "")
	if rec.Code != http.StatusNotFound || problemType(t, rec) != "urn:agentsone:problem:not-found" {
		t.Fatalf("GET = %d %s", rec.Code, rec.Body)
	}
}

func TestRoutes_PutThenGetReturnsTheHash(t *testing.T) {
	t.Parallel()
	s := credstore.New(t.TempDir())

	if rec := serve(t, s, http.MethodPut, `{"hash":"`+hash+`"}`); rec.Code != http.StatusCreated {
		t.Fatalf("PUT = %d %s", rec.Code, rec.Body)
	}

	rec := serve(t, s, http.MethodGet, "")
	var got struct{ Hash string }
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || got.Hash != hash {
		t.Fatalf("GET = %d %s", rec.Code, rec.Body)
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("GET is cacheable")
	}
}

func TestRoutes_PutRefusesASecondHash(t *testing.T) {
	t.Parallel()
	s := credstore.New(t.TempDir())
	serve(t, s, http.MethodPut, `{"hash":"`+hash+`"}`)

	rec := serve(t, s, http.MethodPut, `{"hash":"`+hash+`"}`)

	if rec.Code != http.StatusConflict || problemType(t, rec) != "urn:agentsone:problem:conflict" {
		t.Fatalf("second PUT = %d %s", rec.Code, rec.Body)
	}
}

func TestRoutes_PutRefusesABadBody(t *testing.T) {
	t.Parallel()
	for _, body := range []string{`{"hash":"plain"}`, `{"hash":"` + hash + `","extra":1}`, `not json`, `{"hash":"` + strings.Repeat("a", 20000) + `"}`} {
		rec := serve(t, credstore.New(t.TempDir()), http.MethodPut, body)
		if rec.Code != http.StatusBadRequest || problemType(t, rec) != "urn:agentsone:problem:validation" {
			t.Errorf("PUT %.30q = %d %s", body, rec.Code, rec.Body)
		}
	}
}

func TestRoutes_DeleteClearsTheHash(t *testing.T) {
	t.Parallel()
	s := credstore.New(t.TempDir())
	serve(t, s, http.MethodPut, `{"hash":"`+hash+`"}`)

	if rec := serve(t, s, http.MethodDelete, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE = %d", rec.Code)
	}
	if rec := serve(t, s, http.MethodGet, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("GET after DELETE = %d", rec.Code)
	}
}

func TestRoutes_AStoreThatFailsIsAnInternalProblemWithoutDetail(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, credstore.FileName), []byte("garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	rec := serve(t, credstore.New(dir), http.MethodGet, "")
	if rec.Code != http.StatusInternalServerError || problemType(t, rec) != "urn:agentsone:problem:internal" {
		t.Fatalf("GET = %d %s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), dir) {
		t.Fatal("the problem names a file path")
	}
}
