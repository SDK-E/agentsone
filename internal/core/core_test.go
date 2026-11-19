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
	calls := map[string]func() error{
		"GetAgent":   func() error { _, err := p.Agents.GetAgent(ctx, "a"); return err },
		"ListAgents": func() error { _, err := p.Agents.ListAgents(ctx); return err },
		"Manager":    func() error { _, err := p.Agents.Manager(ctx, "a"); return err },
		"IsBelow":    func() error { _, err := p.Agents.IsBelow(ctx, "a", "b"); return err },
		"TeamRoot":   func() error { _, err := p.Agents.TeamRoot(ctx, "a"); return err },
		"Wake":       func() error { return p.Waker.Wake(ctx, "a", core.WakeTask, nil) },
		"Publish":    func() error { _, err := p.Events.Publish(ctx, core.Event{}); return err },
		"Subscribe":  func() error { _, err := p.Subscribe.Subscribe(ctx, core.EventFilter{}); return err },
		"CreateTask": func() error { _, err := p.Tasks.CreateTask(ctx, core.NewTask{}); return err },
		"Append":     func() error { return p.Audit.Append(ctx, core.AuditEntry{}) },
		"UpdateLine": func() error { return p.Live.UpdateLine(ctx, "a", "line") },
		"Lines":      func() error { _, err := p.Live.Lines(ctx); return err },
		"Allocation": func() error { _, err := p.Budget.Allocation(ctx, "a"); return err },
		"Runner":     func() error { _, err := p.Runners.Runner(ctx, "echo"); return err },
		"Runners":    func() error { _, err := p.Runners.Runners(ctx); return err },
	}
	for name, call := range calls {
		if err := call(); !errors.Is(err, core.ErrUnavailable) {
			t.Errorf("%s: %v, want ErrUnavailable", name, err)
		}
	}
}

func TestFormatSequenceSortsAsNumbers(t *testing.T) {
	cases := map[uint64]string{
		0:                    "00000000000000000000",
		1:                    "00000000000000000001",
		18446744073709551615: "18446744073709551615",
	}
	for n, want := range cases {
		if got := core.FormatSequence(n); got != want {
			t.Errorf("FormatSequence(%d) = %s, want %s", n, got, want)
		}
	}
	if core.FormatSequence(9) >= core.FormatSequence(10) {
		t.Fatal("string order differs from number order")
	}
}
