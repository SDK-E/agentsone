// SPDX-License-Identifier: MIT

package core_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/core"
)

func TestProblemFrom(t *testing.T) {
	cases := []struct {
		err  error
		want int
	}{
		{fmt.Errorf("agent bob: %w", core.ErrNotFound), http.StatusNotFound},
		{fmt.Errorf("x: %w", core.ErrForbidden), http.StatusForbidden},
		{core.ErrNotImplemented, http.StatusNotImplemented},
		{core.NewProblem(http.StatusConflict, "conflict", "taken"), http.StatusConflict},
		{errors.New("secret internal detail"), http.StatusInternalServerError},
	}
	for _, c := range cases {
		p := core.ProblemFrom(c.err)
		if p.Status != c.want {
			t.Errorf("%v: status %d, want %d", c.err, p.Status, c.want)
		}
	}
	if p := core.ProblemFrom(errors.New("secret")); p.Detail != "" {
		t.Errorf("500 leaked detail %q", p.Detail)
	}
}

func TestWriteProblem(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/agents", nil)
	core.WriteProblem(rec, req, core.NotImplemented("listAgents"))
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("code %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("content type %q", ct)
	}
	var p core.Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	if p.Instance != "/v1/agents" || p.Type != core.ProblemTypeBase+"not-implemented" {
		t.Fatalf("problem %+v", p)
	}
}

func TestCaller(t *testing.T) {
	if _, ok := core.CallerFrom(context.Background()); ok {
		t.Fatal("caller on empty context")
	}
	ctx := core.WithCaller(context.Background(), core.Caller{Kind: core.CallerAgent, Name: "founder", Scopes: []string{"chart.write"}})
	c, ok := core.CallerFrom(ctx)
	if !ok || c.Name != "founder" || !c.Has("chart.write") || c.Has("budget.write") {
		t.Fatalf("caller %+v", c)
	}
	if !(core.Caller{IsRoot: true}).Has("anything") {
		t.Fatal("root lacks scope")
	}
}

func TestLoadConfig(t *testing.T) {
	dir := t.TempDir()
	c, err := core.LoadConfig(filepath.Join(dir, "missing.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if c.API.Addr != core.DefaultAddr || c.SocketPath() != "/run/agentsone/api.sock" {
		t.Fatalf("defaults %+v", c)
	}
	if got := c.RunnerSocket("bob"); got != "/run/agentsone/runners/bob.sock" {
		t.Fatalf("runner socket %s", got)
	}
	if got := c.DBPath("memory"); got != "/var/lib/agentsone/memory.db" {
		t.Fatalf("db path %s", got)
	}

	p := filepath.Join(dir, "config.toml")
	body := "state_dir = \"/tmp/a1\"\n[api]\naddr = \"127.0.0.1:9\"\n[budget]\ncheck_interval = \"5s\"\n"
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err = core.LoadConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.StateDir != "/tmp/a1" || c.API.Addr != "127.0.0.1:9" || c.Budget.CheckInterval.Duration != 5*time.Second || c.Log.Level != "info" {
		t.Fatalf("loaded %+v", c)
	}

	if err := os.WriteFile(p, []byte("typo = 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := core.LoadConfig(p); err == nil {
		t.Fatal("unknown key accepted")
	}
}

func TestPortsUnavailable(t *testing.T) {
	p := core.NewPorts()
	ctx := context.Background()
	if _, err := p.Agents.ListAgents(ctx); !errors.Is(err, core.ErrUnavailable) {
		t.Fatalf("agents: %v", err)
	}
	if err := p.Audit.Append(ctx, core.AuditEntry{}); !errors.Is(err, core.ErrUnavailable) {
		t.Fatalf("audit: %v", err)
	}
	if _, err := p.Events.Publish(ctx, core.Event{}); !errors.Is(err, core.ErrUnavailable) {
		t.Fatalf("events: %v", err)
	}
}
