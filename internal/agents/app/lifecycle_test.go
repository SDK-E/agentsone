// SPDX-License-Identifier: MIT

package app_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/agents/app"
	"github.com/SDK-E/agentsone/internal/agents/domain"
	"github.com/SDK-E/agentsone/internal/core"
)

// What the runner does shows on the agent, and the state frame goes out
// when the shown state changes, not on every word.
func TestSetRunState_ShowsWhatTheRunnerDoes(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	frames := e.society.Hub().Subscribe(ctx, "dev", app.StreamOutput)

	for _, st := range []domain.State{domain.StateAwake, domain.StateWorking, domain.StateAwake} {
		if err := e.society.SetRunState(ctx, "dev", st); err != nil {
			t.Fatal(err)
		}
		if f := receive(t, frames); f.State == nil || f.State.State != st {
			t.Fatalf("frame %+v, want state %s", f.State, st)
		}
		got, err := e.society.Get(asHuman("ada"), "dev")
		if err != nil || got.State != st {
			t.Fatalf("Get = %+v, %v; want state %s", got, err, st)
		}
	}

	if err := e.society.SetRunState(ctx, "dev", domain.StateSleeping); err != nil {
		t.Fatal(err)
	}
	got, _ := e.society.Get(asHuman("ada"), "dev")
	if got.State != domain.StateSleeping {
		t.Fatalf("state after the runner stopped = %s, want sleeping", got.State)
	}
}

func TestSetRunState_AFailureStaysUntilTheRunnerRunsAgain(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()
	if err := e.society.RunnerFailed(ctx, "dev", gatewayReason); err != nil {
		t.Fatal(err)
	}

	if err := e.society.SetRunState(ctx, "dev", domain.StateWaking); err != nil {
		t.Fatal(err)
	}

	got, _ := e.society.Get(asHuman("ada"), "dev")
	if got.State != domain.StateFailed {
		t.Fatalf("state = %s, want failed to stay over a starting runner", got.State)
	}
}

func TestSetRunState_RefusesAStateThatIsNotARunState(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	for _, st := range []domain.State{domain.StateFrozen, domain.StateRetired, domain.StateFailed} {
		if err := e.society.SetRunState(context.Background(), "dev", st); !errors.Is(err, core.ErrInvalid) {
			t.Errorf("SetRunState(%s) = %v, want ErrInvalid", st, err)
		}
	}
}

func TestSetRunState_AnUnknownAgentIsNotFound(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	if err := e.society.SetRunState(context.Background(), "nobody", domain.StateAwake); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("SetRunState = %v, want ErrNotFound", err)
	}
}

func TestProfiles_ShowTheRunState(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()
	if err := e.society.SetRunState(ctx, "dev", domain.StateAwake); err != nil {
		t.Fatal(err)
	}

	profiles, err := e.society.Profiles().Profiles(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range profiles {
		if p.Name == "dev" && p.State != string(domain.StateAwake) {
			t.Fatalf("profile of dev shows %q, want awake", p.State)
		}
	}
}

func TestTurnEnded_IsStoredWithTheAgent(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	at := time.Date(2026, 11, 20, 5, 40, 0, 0, time.FixedZone("x", 3600))

	if err := e.society.TurnEnded(context.Background(), "dev", at); err != nil {
		t.Fatal(err)
	}

	got, err := e.society.Get(asHuman("ada"), "dev")
	if err != nil || got.LastTurnAt == nil || !got.LastTurnAt.Equal(at) {
		t.Fatalf("Get = %+v, %v; want last turn %s", got, err, at)
	}
	if err := e.society.TurnEnded(context.Background(), "nobody", at); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("TurnEnded of an unknown agent = %v, want ErrNotFound", err)
	}
}

func TestStartingLine(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("é", core.MaxLiveLine)
	cases := []struct{ name, role, want string }{
		{"a role is the line", "Reviews pull requests", "Reviews pull requests"},
		{"no role is new", "", "new"},
		{"blank role is new", "  \n\t ", "new"},
		{"lines and spaces collapse", "Reviews\n  pull   requests", "Reviews pull requests"},
	}
	for _, c := range cases {
		if got := app.StartingLine(c.role); got != c.want {
			t.Errorf("%s: StartingLine(%q) = %q, want %q", c.name, c.role, got, c.want)
		}
	}
	got := app.StartingLine(long)
	if len(got) > core.MaxLiveLine || !strings.HasPrefix(long, got) || got == "" {
		t.Errorf("long role: line of %d bytes, want a prefix within %d", len(got), core.MaxLiveLine)
	}
}

// A new agent shows its role as its one line until its first turn; the
// agent's own line then replaces it.
func TestHire_GivesTheNewAgentItsRoleAsItsLine(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	if _, err := e.society.Hire(asAgent("lead"), spec("mira")); err != nil {
		t.Fatal(err)
	}

	if got := e.line(t, "mira"); got != "Reviewer" {
		t.Fatalf("line = %q, want the role", got)
	}
	if err := e.ports.Live.UpdateLine(context.Background(), "mira", "Goal: review. Now: reading."); err != nil {
		t.Fatal(err)
	}
	if got := e.line(t, "mira"); got != "Goal: review. Now: reading." {
		t.Fatalf("line = %q, want the agent's own", got)
	}
}

func TestFillStartingLines_GivesLineToAgentsThatHaveNoneAndKeepsTheRest(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()
	if err := e.ports.Live.UpdateLine(ctx, "dev", "Goal: ship. Now: coding."); err != nil {
		t.Fatal(err)
	}

	if err := e.society.FillStartingLines(ctx); err != nil {
		t.Fatal(err)
	}

	if got := e.line(t, "dev"); got != "Goal: ship. Now: coding." {
		t.Fatalf("line of dev = %q, want its own kept", got)
	}
	chart, err := e.society.Chart(asHuman("ada"))
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range chart.Agents() {
		if a.Active() && e.line(t, a.Name) == "" {
			t.Errorf("active agent %s has no line", a.Name)
		}
	}
}

// line is the agent's line in the live list, or "" when it has none.
func (e env) line(t *testing.T, agent string) string {
	t.Helper()
	lines, err := e.ports.Live.Lines(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range lines {
		if l.Agent == agent {
			return l.Line
		}
	}
	return ""
}
