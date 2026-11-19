// SPDX-License-Identifier: MIT

package app_test

import (
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/SDK-E/agentsone/internal/agents/domain"
)

// An always-on agent's runner starts as soon as it is hired, with no turn;
// a chat agent's runner waits for its first wake.
func TestHire_StartsAnAlwaysOnAgentsRunnerIdle(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	keeper := spec("keeper")
	keeper.Kind = domain.KindAlwaysOn
	if _, err := e.society.Hire(asAgent("lead"), keeper); err != nil {
		t.Fatal(err)
	}
	if _, err := e.society.Hire(asAgent("lead"), spec("mira")); err != nil {
		t.Fatal(err)
	}

	if diff := cmp.Diff([]string{"keeper"}, e.rt.Ran()); diff != "" {
		t.Errorf("runners started (-want +got):\n%s", diff)
	}
	if len(e.rt.Wakes()) != 0 {
		t.Errorf("wakes = %v, want none", e.rt.Wakes())
	}
}

// A runner that cannot start does not undo the hire: the agent exists and
// its status says why it stopped.
func TestHire_KeepsTheAgentWhenItsRunnerCannotStart(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.rt.FailRun = errors.New("model gateway not reachable")
	keeper := spec("keeper")
	keeper.Kind = domain.KindAlwaysOn
	if _, err := e.society.Hire(asAgent("lead"), keeper); err != nil {
		t.Fatalf("Hire = %v, want the agent hired", err)
	}
	if _, err := e.repo.GetAgent(asAgent("lead"), "keeper"); err != nil {
		t.Fatal(err)
	}
}

// RunAlwaysOn starts every active always-on agent, for the daemon's start.
func TestRunAlwaysOn_StartsEveryActiveAlwaysOnAgent(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	role := "Retired"
	if _, err := e.society.Update(asAgent("lead"), "intern", domain.Update{Role: &role}); err != nil {
		t.Fatal(err)
	}
	if err := e.society.Retire(asAgent("dev"), "intern"); err != nil {
		t.Fatal(err)
	}
	if err := e.society.RunAlwaysOn(asAgent("lead")); err != nil {
		t.Fatal(err)
	}
	// The seeded agents are always-on but the waker; intern is retired.
	if diff := cmp.Diff([]string{"dev", "founder", "lead", "ops"}, e.rt.Ran()); diff != "" {
		t.Errorf("runners started (-want +got):\n%s", diff)
	}
}
