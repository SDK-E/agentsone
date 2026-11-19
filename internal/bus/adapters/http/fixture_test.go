// SPDX-License-Identifier: MIT

package httpadapter_test

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	httpadapter "github.com/SDK-E/agentsone/internal/bus/adapters/http"
	"github.com/SDK-E/agentsone/internal/bus/adapters/memory"
	"github.com/SDK-E/agentsone/internal/bus/app"
	"github.com/SDK-E/agentsone/internal/contract/contracttest"
	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/core/coretest"
)

var (
	human    = core.Caller{Kind: core.CallerHuman, Name: "hsaddek", UID: 1001}
	founder  = core.Caller{Kind: core.CallerAgent, Name: "founder", UID: 1000}
	builder  = core.Caller{Kind: core.CallerAgent, Name: "builder", UID: 1002}
	intern   = core.Caller{Kind: core.CallerAgent, Name: "intern", UID: 1003}
	outsider = core.Caller{Kind: core.CallerAgent, Name: "outsider", UID: 1004}
	plugin   = core.Caller{Kind: core.CallerPlugin, Name: "plg_01JD7ZK3M00000000000000001"}
)

// env is the real bus service on in-memory repositories behind the
// generated routes.
type env struct {
	svc   *app.Service
	mux   *http.ServeMux
	clock *coretest.Clock
	fakes *coretest.Fakes
	// ticks drives the stream heartbeat.
	ticks chan time.Time
}

// newEnv wires the chart founder > builder > intern, plus outsider alone.
func newEnv(t *testing.T) *env {
	t.Helper()
	fakes, ports := coretest.NewFakes()
	fakes.Agents.Add(core.Agent{Name: "founder", Kind: core.AgentAlwaysOn})
	fakes.Agents.Add(core.Agent{Name: "builder", Kind: core.AgentChat, Manager: "founder"})
	fakes.Agents.Add(core.Agent{Name: "intern", Kind: core.AgentChat, Manager: "builder"})
	fakes.Agents.Add(core.Agent{Name: "outsider", Kind: core.AgentChat})
	svc := app.New(app.Deps{
		Channels: memory.NewChannels(), Messages: memory.NewMessages(), Clock: fakes.Clock, IDs: fakes.IDs, Ports: ports,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	e := &env{svc: svc, mux: http.NewServeMux(), clock: fakes.Clock, fakes: fakes, ticks: make(chan time.Time)}
	ticker := func(time.Duration) (<-chan time.Time, func()) { return e.ticks, func() {} }
	opts := core.APIOptions{
		RequestError: func(w http.ResponseWriter, r *http.Request, err error) {
			core.WriteProblem(w, r, core.NewProblem(http.StatusBadRequest, "invalid", err.Error()))
		},
		ResponseError: core.WriteError,
	}
	httpadapter.Group(svc, fakes.Clock, httpadapter.WithTicker(ticker)).Register(e.mux, opts)
	return e
}

// as returns a handler that runs the routes as the caller.
func (e *env) as(c core.Caller) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		e.mux.ServeHTTP(w, r.WithContext(core.WithCaller(r.Context(), c)))
	})
}

// do sends one request as the caller and checks the response against the
// contract.
func (e *env) do(t *testing.T, c core.Caller, method, target string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, target, reader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	e.as(c).ServeHTTP(rec, req)
	contracttest.CheckResponse(t, req, rec)
	return rec
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder, wantStatus int) T {
	t.Helper()
	if rec.Code != wantStatus {
		t.Fatalf("status = %d, want %d: %s", rec.Code, wantStatus, rec.Body)
	}
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode %s: %v", rec.Body, err)
	}
	return v
}

func problemType(t *testing.T, rec *httptest.ResponseRecorder, wantStatus int) string {
	t.Helper()
	p := decode[struct {
		Type string `json:"type"`
	}](t, rec, wantStatus)
	return p.Type
}

type obj = map[string]any
