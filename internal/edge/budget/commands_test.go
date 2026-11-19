// SPDX-License-Identifier: MIT

package budget

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/edge/adminapi"
	budgetapp "github.com/SDK-E/agentsone/internal/edge/budget/app"
	"github.com/SDK-E/agentsone/internal/edge/edgeenv"
	"github.com/SDK-E/agentsone/internal/edge/gateway/adapters/admin"
)

type memStore struct {
	set budgetapp.Settings
	sp  budgetapp.Spend
}

func (m *memStore) LoadSettings(context.Context) (budgetapp.Settings, error) { return m.set, nil }
func (m *memStore) SaveSettings(_ context.Context, s budgetapp.Settings) error {
	m.set = s
	return nil
}
func (m *memStore) LoadSpend(context.Context) (budgetapp.Spend, error) { return m.sp, nil }
func (m *memStore) SaveSpend(_ context.Context, s budgetapp.Spend) error {
	m.sp = s
	return nil
}

type clock struct{}

func (clock) Now() time.Time { return time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC) }

// edge is a running edge's budget routes, served over HTTP for the commands.
func edge(t *testing.T) *adminapi.Client {
	t.Helper()
	svc, err := budgetapp.Open(t.Context(), &memStore{}, clock{})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	admin.Register(mux, admin.Options{Budget: svc})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return adminapi.NewHTTPClient(srv.URL, srv.Client())
}

func run(t *testing.T, c adminClient, args ...string) (string, error) {
	t.Helper()
	var out, errOut bytes.Buffer
	var socket string
	env := &edgeenv.Env{Out: &out, Err: &errOut, Log: slog.New(slog.DiscardHandler)}
	cmds := commands(env, func(s string) adminClient { socket = s; return c })
	if len(cmds) != 1 || cmds[0].Name() != "budget" {
		t.Fatalf("commands = %v", cmds)
	}
	cmd := cmds[0]
	cmd.SetArgs(args)
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	err := cmd.ExecuteContext(t.Context())
	if socket != "" && socket != "/run/agentsone-edge-admin/admin.sock" {
		t.Errorf("socket = %q", socket)
	}
	return out.String(), err
}

func TestSetShowPeriodAndDefence(t *testing.T) {
	t.Parallel()
	c := edge(t)
	out, err := run(t, c, "show")
	if err != nil || !strings.Contains(out, "Total budget:     $0.00") || !strings.Contains(out, "one month, 1 Nov 2026 to 1 Dec 2026") {
		t.Fatalf("show on a fresh edge: %v\n%s", err, out)
	}
	if out, err := run(t, c, "set", "25.50"); err != nil || !strings.Contains(out, "$25.50") {
		t.Fatalf("set: %v\n%s", err, out)
	}
	if out, err := run(t, c, "period", "week"); err != nil || !strings.Contains(out, "one week") {
		t.Fatalf("period: %v\n%s", err, out)
	}
	if out, err := run(t, c, "defence", "share", "20"); err != nil || !strings.Contains(out, "20%") {
		t.Fatalf("defence share: %v\n%s", err, out)
	}
	if out, err := run(t, c, "defence", "on"); err != nil || !strings.Contains(out, "on") {
		t.Fatalf("defence on: %v\n%s", err, out)
	}
	out, err = run(t, c, "show")
	if err != nil {
		t.Fatalf("show: %v", err)
	}
	for _, want := range []string{"$25.50", "one week, 16 Nov 2026 to 23 Nov 2026", "on, 20%", "$5.10", "Left for agents:  $20.40", "Left for defence: $25.50"} {
		if !strings.Contains(out, want) {
			t.Fatalf("show is missing %q:\n%s", want, out)
		}
	}
	if out, err := run(t, c, "defence", "off"); err != nil || !strings.Contains(out, "off") {
		t.Fatalf("defence off: %v\n%s", err, out)
	}
	if out, _ := run(t, c, "show"); !strings.Contains(out, "off (share set to 20%)") {
		t.Fatalf("show after off:\n%s", out)
	}
}

func TestShowSaysWhenTheBudgetIsSpent(t *testing.T) {
	t.Parallel()
	out, err := run(t, edge(t), "show")
	if err != nil || !strings.Contains(out, "refused until the budget is raised or the next period starts") {
		t.Fatalf("show: %v\n%s", err, out)
	}
}

func TestBadInput(t *testing.T) {
	t.Parallel()
	c := edge(t)
	for _, args := range [][]string{
		{"set", "lots"},
		{"set"},
		{"period", "year"},
		{"defence", "share", "120"},
		{"defence", "share", "x"},
		{"defence", "maybe"},
	} {
		if _, err := run(t, c, args...); err == nil {
			t.Errorf("budget %v succeeded", args)
		}
	}
}

func TestEdgeDown(t *testing.T) {
	t.Parallel()
	c := adminapi.NewClient(t.TempDir() + "/none.sock")
	if _, err := run(t, c, "show"); err == nil || !strings.Contains(err.Error(), "is the edge running") {
		t.Fatalf("show with no edge error = %v", err)
	}
}

func TestHelp(t *testing.T) {
	t.Parallel()
	if out, err := run(t, edge(t)); err != nil || !strings.Contains(out, "Usage:") {
		t.Fatalf("budget: %v\n%s", err, out)
	}
	if Commands(&edgeenv.Env{})[0].Name() != "budget" {
		t.Error("Commands() has no budget command")
	}
}
