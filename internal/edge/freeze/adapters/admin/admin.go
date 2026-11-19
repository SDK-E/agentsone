// SPDX-License-Identifier: MIT

// Package admin serves the freeze-all switch on the admin socket, which only the control
// container reaches, and gates model calls on it.
//
//	GET    /v1/freeze   the switch: {"frozen": bool, "since": time or absent}
//	POST   /v1/freeze   freeze all (idempotent)
//	DELETE /v1/freeze   release the freeze (idempotent)
package admin

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/SDK-E/agentsone/internal/edge/freeze/domain"
)

// Switch is the freeze-all switch.
type Switch interface {
	Freeze(ctx context.Context) error
	Release(ctx context.Context) error
	Frozen(ctx context.Context) (bool, error)
	Status() domain.State
}

// Problem types.
const (
	ProblemFrozen   = "urn:agentsone:problem:frozen"
	ProblemInternal = "urn:agentsone:problem:internal"
)

type statusBody struct {
	Frozen bool    `json:"frozen"`
	Since  *string `json:"since,omitempty"`
}

// Register adds the freeze routes to mux.
func Register(mux *http.ServeMux, sw Switch, log *slog.Logger) {
	show := func(w http.ResponseWriter) {
		st := sw.Status()
		body := statusBody{Frozen: st.Frozen}
		if !st.Since.IsZero() {
			s := st.Since.UTC().Format(time.RFC3339Nano)
			body.Since = &s
		}
		writeJSON(w, http.StatusOK, "application/json", body)
	}
	change := func(do func(context.Context) error, what string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if err := do(r.Context()); err != nil {
				log.Error("freeze: "+what+" failed", "err", err)
				writeProblem(w, http.StatusInternalServerError, ProblemInternal, "Freeze change incomplete",
					"Part of the change failed; see the edge log. The switch stays on until a release succeeds.")
				return
			}
			show(w)
		}
	}
	mux.HandleFunc("GET /v1/freeze", func(w http.ResponseWriter, _ *http.Request) { show(w) })
	mux.HandleFunc("POST /v1/freeze", change(sw.Freeze, "freeze"))
	mux.HandleFunc("DELETE /v1/freeze", change(sw.Release, "release"))
}

// Frozen tells whether the switch is on.
type Frozen interface {
	Frozen(ctx context.Context) (bool, error)
}

// GateModelCalls wraps the gateway's handler: while the switch is on, every call is refused with
// 423 and problem type urn:agentsone:problem:frozen. There is no Retry-After: only a human ends a
// freeze. When the switch cannot say, the call is refused too.
func GateModelCalls(f Frozen, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if on, err := f.Frozen(r.Context()); err != nil || on {
			w.Header().Del("Retry-After")
			writeProblem(w, http.StatusLocked, ProblemFrozen, "Frozen",
				"A human froze all agents. Model calls are off until a human releases the freeze.")
			return
		}
		next.ServeHTTP(w, r)
	})
}

type problem struct {
	Type   string `json:"type"`
	Title  string `json:"title"`
	Status int    `json:"status"`
	Detail string `json:"detail"`
}

func writeProblem(w http.ResponseWriter, code int, typ, title, detail string) {
	writeJSON(w, code, "application/problem+json", problem{Type: typ, Title: title, Status: code, Detail: detail})
}

func writeJSON(w http.ResponseWriter, code int, contentType string, v any) {
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
