// SPDX-License-Identifier: MIT

package router_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/core/coretest"
	"github.com/SDK-E/agentsone/internal/core/moduletest"
	"github.com/SDK-E/agentsone/internal/router"
)

func TestModuleOwnsItsTags(t *testing.T) {
	deps, _ := coretest.NewDeps(t)
	m := router.New(deps)
	if m.Name() != "router" {
		t.Fatalf("name %q", m.Name())
	}
	moduletest.AssertOwnsTags(t, m, "router")
}

func TestModuleProvidesTheLiveListAndServesIt(t *testing.T) {
	deps, _ := coretest.NewDeps(t)
	m := router.New(deps)
	if err := m.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	ports := core.NewPorts()
	m.Provide(ports)

	// With no agent directory the port still takes a line and the list read
	// reports the society as unavailable instead of crashing.
	if err := ports.Live.UpdateLine(context.Background(), "builder", "Goal: ship. Now: coding."); err != nil {
		t.Fatal(err)
	}
	lines, err := ports.Live.Lines(context.Background())
	if err != nil || len(lines) != 1 {
		t.Fatalf("lines = %v, %v", lines, err)
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/router/live", nil)
	req = req.WithContext(core.WithCaller(req.Context(), core.Caller{Kind: core.CallerHuman, Name: "ada", UID: -1}))
	rec := httptest.NewRecorder()
	moduletest.Mount(m).ServeHTTP(rec, req)
	if rec.Code == http.StatusNotImplemented {
		t.Fatalf("live list still answers 501")
	}
}

func TestModuleInterimAdaptersFailClosedWithoutPorts(t *testing.T) {
	deps, _ := coretest.NewDeps(t)
	m := router.New(deps)
	req := httptest.NewRequest(http.MethodPost, "/v1/router/route", nil)
	req = req.WithContext(core.WithCaller(req.Context(), core.Caller{Kind: core.CallerHuman, Name: "ada", UID: -1}))
	rec := httptest.NewRecorder()
	moduletest.Mount(m).ServeHTTP(rec, req)
	if rec.Code == http.StatusInternalServerError || rec.Code == http.StatusNotImplemented {
		t.Fatalf("status %d", rec.Code)
	}
}
