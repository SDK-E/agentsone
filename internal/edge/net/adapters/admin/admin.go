// SPDX-License-Identifier: MIT

// Package admin holds the internet filter's routes on the edge admin socket: read and edit the
// allow list, and read the egress freeze switch. The control side calls them; the machine never
// reaches the admin socket. The routes and their types are in package adminapi.
//
// The switch is flipped only by the edge freeze component, which audits the change and also stops
// model calls; it has no route here.
package admin

import (
	"context"
	"errors"
	"net/http"

	"github.com/SDK-E/agentsone/internal/edge/adminapi"
	"github.com/SDK-E/agentsone/internal/edge/net/app"
	"github.com/SDK-E/agentsone/internal/edge/net/domain"
)

// Editor reads and edits the allow list. *app.Editor is one.
type Editor interface {
	List(ctx context.Context) ([]domain.Entry, error)
	Add(ctx context.Context, pattern, why string) (domain.Entry, error)
	Remove(ctx context.Context, pattern string) error
}

// FreezeReader reads the egress freeze switch. *app.Filter is one.
type FreezeReader interface {
	Frozen(ctx context.Context) (bool, error)
}

// Register adds the routes to mux.
func Register(mux *http.ServeMux, editor Editor, freeze FreezeReader) {
	h := handlers{editor: editor, freeze: freeze}
	mux.HandleFunc(adminapi.RouteAllowList, h.list)
	mux.HandleFunc(adminapi.RouteAddAllowed, h.add)
	mux.HandleFunc(adminapi.RouteRemoveAllowed, h.remove)
	mux.HandleFunc(adminapi.RouteEgressFreeze, h.frozen)
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
	out := adminapi.AllowList{Entries: make([]adminapi.AllowEntry, 0, len(entries))}
	for _, e := range entries {
		out.Entries = append(out.Entries, adminapi.AllowEntry{Pattern: e.Pattern.String(), Why: e.Why})
	}
	adminapi.WriteJSON(w, http.StatusOK, out)
}

func (h handlers) add(w http.ResponseWriter, r *http.Request) {
	var in adminapi.AllowEntry
	if p := adminapi.Decode(r, &in); p != nil {
		adminapi.WriteProblem(w, p)
		return
	}
	e, err := h.editor.Add(r.Context(), in.Pattern, in.Why)
	if err != nil {
		writeError(w, err)
		return
	}
	adminapi.WriteJSON(w, http.StatusCreated, adminapi.AllowEntry{Pattern: e.Pattern.String(), Why: e.Why})
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
	adminapi.WriteJSON(w, http.StatusOK, adminapi.EgressFreeze{Frozen: frozen})
}

// writeError maps domain errors to problems. Anything else is an internal error, reported without
// its text.
func writeError(w http.ResponseWriter, err error) {
	var p *adminapi.Problem
	switch {
	case errors.Is(err, domain.ErrInvalidPattern), errors.Is(err, domain.ErrInvalidName), errors.Is(err, app.ErrInvalidReason):
		p = adminapi.NewProblem(http.StatusBadRequest, adminapi.SlugValidation, err.Error())
	case errors.Is(err, domain.ErrDuplicateEntry):
		p = adminapi.NewProblem(http.StatusConflict, adminapi.SlugConflict, err.Error())
	case errors.Is(err, domain.ErrEntryNotFound):
		p = adminapi.NewProblem(http.StatusNotFound, adminapi.SlugNotFound, err.Error())
	default:
		p = adminapi.NewProblem(http.StatusInternalServerError, adminapi.SlugInternal, "The edge could not do this. Its log has the reason.")
	}
	adminapi.WriteProblem(w, p)
}
