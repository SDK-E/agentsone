// SPDX-License-Identifier: MIT

package sched_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/core/coretest"
	"github.com/SDK-E/agentsone/internal/core/moduletest"
	"github.com/SDK-E/agentsone/internal/sched"
)

func TestModuleOwnsItsTags(t *testing.T) {
	t.Parallel()
	deps, _ := coretest.NewDeps(t)
	m := sched.New(deps)
	if m.Name() != "sched" {
		t.Fatalf("name %q", m.Name())
	}
	moduletest.AssertOwnsTags(t, m, "tasks", "schedules")
}

func TestModuleLifecycle_ServesTasksAndCatchesUpMissedTimersOnStart(t *testing.T) {
	t.Parallel()
	deps, fakes := coretest.NewDeps(t)
	fakes.Agents.Add(core.Agent{Name: "founder", Kind: core.AgentChat, IsRoot: true, Runner: "echo"})
	m := sched.New(deps)
	ctx := context.Background()
	if err := m.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	ports := core.NewPorts()
	m.Provide(ports)
	if ports.Tasks == nil {
		t.Fatal("the module did not provide the Tasks port")
	}
	mux := moduletest.Mount(m)
	send := func(caller core.Caller, method, target, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, target, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req = req.WithContext(core.WithCaller(req.Context(), caller))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}
	founder := core.Caller{Kind: core.CallerAgent, Name: "founder", UID: 1000}

	if rec := send(core.Caller{Kind: core.CallerHuman, Name: "ada", UID: -1}, http.MethodGet, "/v1/tasks", ""); rec.Code != http.StatusOK {
		t.Fatalf("GET /v1/tasks = %d: %s", rec.Code, rec.Body)
	}

	// A timer comes due while the daemon is down; the next start fires it once.
	rec := send(founder, http.MethodPost, "/v1/schedules", `{"name":"morning","calendar":"*-*-* 09:00:00","message":"Check the board."}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /v1/schedules = %d: %s", rec.Code, rec.Body)
	}
	fakes.Clock.Advance(72 * time.Hour)
	if err := m.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if err := m.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	wakes := fakes.Waker.Wakes()
	if len(wakes) != 1 || wakes[0].Agent != "founder" || wakes[0].Reason != core.WakeTimer {
		t.Fatalf("wakes after the restart = %+v, want one timer wake for founder", wakes)
	}
}
