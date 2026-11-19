// SPDX-License-Identifier: MIT

package admin_test

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/edge/freeze/adapters/admin"
	"github.com/SDK-E/agentsone/internal/edge/freeze/domain"
)

type fakeSwitch struct {
	state     domain.State
	failNext  error
	frozenErr error
}

func (f *fakeSwitch) Freeze(context.Context) error {
	f.state = domain.State{Frozen: true, Since: time.Date(2026, 11, 19, 21, 0, 0, 0, time.UTC)}
	return f.failNext
}

func (f *fakeSwitch) Release(context.Context) error {
	if f.failNext != nil {
		return f.failNext
	}
	f.state = domain.State{Since: time.Date(2026, 11, 19, 22, 0, 0, 0, time.UTC)}
	return nil
}

func (f *fakeSwitch) Frozen(context.Context) (bool, error) { return f.state.Frozen, f.frozenErr }

func (f *fakeSwitch) Status() domain.State { return f.state }

func serve(t *testing.T, sw *fakeSwitch, method string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	admin.Register(mux, sw, slog.New(slog.DiscardHandler))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), method, "/v1/freeze", nil))
	return rec
}

type status struct {
	Frozen bool    `json:"frozen"`
	Since  *string `json:"since"`
}

func decode(t *testing.T, rec *httptest.ResponseRecorder, v any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), v); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
}

func TestRoutes_GetShowsTheSwitch(t *testing.T) {
	t.Parallel()
	rec := serve(t, &fakeSwitch{}, http.MethodGet)
	var got status
	decode(t, rec, &got)
	if rec.Code != http.StatusOK || got.Frozen || got.Since != nil {
		t.Fatalf("GET = %d %+v, want 200 open with no since", rec.Code, got)
	}
}

func TestRoutes_PostFreezes(t *testing.T) {
	t.Parallel()
	rec := serve(t, &fakeSwitch{}, http.MethodPost)
	var got status
	decode(t, rec, &got)
	if rec.Code != http.StatusOK || !got.Frozen || got.Since == nil || *got.Since != "2026-11-19T21:00:00Z" {
		t.Fatalf("POST = %d %+v, want 200 frozen since 21:00", rec.Code, got)
	}
}

func TestRoutes_DeleteReleases(t *testing.T) {
	t.Parallel()
	rec := serve(t, &fakeSwitch{state: domain.State{Frozen: true}}, http.MethodDelete)
	var got status
	decode(t, rec, &got)
	if rec.Code != http.StatusOK || got.Frozen {
		t.Fatalf("DELETE = %d %+v, want 200 open", rec.Code, got)
	}
}

func TestRoutes_FailureIsAProblemThatLeaksNothing(t *testing.T) {
	t.Parallel()
	for _, method := range []string{http.MethodPost, http.MethodDelete} {
		sw := &fakeSwitch{failNext: errors.New("open /var/lib/agentsone-edge/freeze.json: disk full")}
		rec := serve(t, sw, method)
		body := rec.Body.String()
		if rec.Code != http.StatusInternalServerError || rec.Header().Get("Content-Type") != "application/problem+json" {
			t.Errorf("%s = %d %q, want 500 problem", method, rec.Code, rec.Header().Get("Content-Type"))
		}
		if strings.Contains(body, "/var/lib") || !strings.Contains(body, "urn:agentsone:problem:internal") {
			t.Errorf("%s body = %s, want internal problem without the path", method, body)
		}
	}
}

func TestRoutes_OtherMethodsAreRefused(t *testing.T) {
	t.Parallel()
	if rec := serve(t, &fakeSwitch{}, http.MethodPut); rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("PUT = %d, want 405", rec.Code)
	}
}

func gated(t *testing.T, sw *fakeSwitch) *httptest.ResponseRecorder {
	t.Helper()
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	rec := httptest.NewRecorder()
	admin.GateModelCalls(sw, next).ServeHTTP(rec,
		httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/chat/completions", nil))
	return rec
}

func TestGate_PassesCallsWhileOpen(t *testing.T) {
	t.Parallel()
	if rec := gated(t, &fakeSwitch{}); rec.Code != http.StatusTeapot {
		t.Fatalf("open gate = %d, want the next handler", rec.Code)
	}
}

func TestGate_RefusesCallsWhileFrozen(t *testing.T) {
	t.Parallel()
	rec := gated(t, &fakeSwitch{state: domain.State{Frozen: true}})
	var p struct {
		Type   string `json:"type"`
		Status int    `json:"status"`
	}
	decode(t, rec, &p)
	if rec.Code != http.StatusLocked || p.Type != "urn:agentsone:problem:frozen" || p.Status != http.StatusLocked {
		t.Fatalf("frozen gate = %d %+v, want 423 frozen problem", rec.Code, p)
	}
	if got := rec.Header().Get("Retry-After"); got != "" {
		t.Fatalf("Retry-After = %q, want none: only a human ends a freeze", got)
	}
}

func TestGate_FailsClosedWhenTheSwitchCannotSay(t *testing.T) {
	t.Parallel()
	if rec := gated(t, &fakeSwitch{frozenErr: errors.New("boom")}); rec.Code != http.StatusLocked {
		t.Fatalf("unknown switch = %d, want 423", rec.Code)
	}
}
