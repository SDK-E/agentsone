// SPDX-License-Identifier: MIT

// Package admin holds the internet filter's routes on the edge admin socket: read and edit the
// allow list, and read the egress freeze switch. The control side calls them; the machine never
// reaches the admin socket.
//
//	GET    /v1/net/allow-list            {"entries":[{"pattern","why"}]}
//	POST   /v1/net/allow-list            {"pattern","why"} -> 201 the entry
//	DELETE /v1/net/allow-list/{pattern}  204
//	GET    /v1/net/freeze                {"frozen":bool}
//
// The switch is flipped only by the edge freeze component, which audits the change and also stops
// model calls; it has no route here.
//
// Errors are RFC 9457 problem documents typed urn:agentsone:problem:<slug>.
package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/SDK-E/agentsone/internal/edge/net/app"
	"github.com/SDK-E/agentsone/internal/edge/net/domain"
)

const maxBody = 16 << 10

// Editor reads and edits the allow list. *app.Editor is one.
type Editor interface {
	List(ctx context.Context) ([]domain.Entry, error)
	Add(ctx context.Context, pattern, why string) (domain.Entry, error)
	Remove(ctx context.Context, pattern string) error
}

type entry struct {
	Pattern string `json:"pattern"`
	Why     string `json:"why"`
}

type entryList struct {
	Entries []entry `json:"entries"`
}

type freezeSwitch struct {
	Frozen bool `json:"frozen"`
}

// FreezeReader reads the egress freeze switch. *app.Filter is one.
type FreezeReader interface {
	Frozen(ctx context.Context) (bool, error)
}

// Register adds the routes to mux.
func Register(mux *http.ServeMux, editor Editor, freeze FreezeReader) {
	h := handlers{editor: editor, freeze: freeze}
	mux.HandleFunc("GET /v1/net/allow-list", h.list)
	mux.HandleFunc("POST /v1/net/allow-list", h.add)
	mux.HandleFunc("DELETE /v1/net/allow-list/{pattern}", h.remove)
	mux.HandleFunc("GET /v1/net/freeze", h.frozen)
}

type handlers struct {
	editor Editor
	freeze FreezeReader
}

func (h handlers) list(w http.ResponseWriter, r *http.Request) {
	entries, err := h.editor.List(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	out := entryList{Entries: make([]entry, 0, len(entries))}
	for _, e := range entries {
		out.Entries = append(out.Entries, entry{Pattern: e.Pattern.String(), Why: e.Why})
	}
	writeJSON(w, http.StatusOK, out)
}

func (h handlers) add(w http.ResponseWriter, r *http.Request) {
	var in entry
	if err := decode(r, &in); err != nil {
		writeProblem(w, http.StatusBadRequest, "validation", err.Error())
		return
	}
	e, err := h.editor.Add(r.Context(), in.Pattern, in.Why)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, entry{Pattern: e.Pattern.String(), Why: e.Why})
}

func (h handlers) remove(w http.ResponseWriter, r *http.Request) {
	if err := h.editor.Remove(r.Context(), r.PathValue("pattern")); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h handlers) frozen(w http.ResponseWriter, r *http.Request) {
	frozen, err := h.freeze.Frozen(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, freezeSwitch{Frozen: frozen})
}

// decode reads one JSON object with no unknown fields.
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
		return fmt.Errorf("the request body is not valid: %w", err)
	}
	return nil
}

// writeError maps domain errors to problems. Anything else is an internal error, reported without
// its text.
func writeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, domain.ErrInvalidPattern), errors.Is(err, domain.ErrInvalidName), errors.Is(err, app.ErrInvalidReason):
		writeProblem(w, http.StatusBadRequest, "validation", err.Error())
	case errors.Is(err, domain.ErrDuplicateEntry):
		writeProblem(w, http.StatusConflict, "conflict", err.Error())
	case errors.Is(err, domain.ErrEntryNotFound):
		writeProblem(w, http.StatusNotFound, "not-found", err.Error())
	default:
		writeProblem(w, http.StatusInternalServerError, "internal", "The edge could not do this. Its log has the reason.")
	}
}

type problem struct {
	Type   string `json:"type"`
	Title  string `json:"title"`
	Status int    `json:"status"`
	Detail string `json:"detail,omitempty"`
}

func writeProblem(w http.ResponseWriter, status int, slug, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(problem{
		Type: "urn:agentsone:problem:" + slug, Title: http.StatusText(status), Status: status, Detail: detail,
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
