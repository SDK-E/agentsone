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

// Releasing a freeze starts the runners of the always-on agents it set
// free, idle, as at the daemon's start. An agent another freeze still
// covers stays stopped, and a waker waits for its wake.
func TestRelease_StartsTheReleasedAlwaysOnAgentsRunners(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	for _, s := range []string{"all", "agent:dev"} {
		if err := e.society.Freezer().Freeze(asHost(), scope(t, s)); err != nil {
			t.Fatal(err)
		}
	}
	if len(e.rt.Ran()) != 0 {
		t.Fatalf("a freeze started runners: %v", e.rt.Ran())
	}

	if err := e.society.Freezer().Release(asHost(), scope(t, "all")); err != nil {
		t.Fatal(err)
	}

	if diff := cmp.Diff([]string{"founder", "intern", "lead", "ops"}, e.rt.Ran()); diff != "" {
		t.Errorf("runners started (-want +got):\n%s", diff)
	}
}

// A runner that cannot start does not undo the release: the supervisor
// records it and puts the reason on the agent's status.
func TestRelease_StandsWhenARunnerCannotStart(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	if err := e.society.Freezer().Freeze(asHost(), scope(t, "agent:ops")); err != nil {
		t.Fatal(err)
	}
	e.rt.FailRun = errors.New("model gateway not reachable")

	if err := e.society.Freezer().Release(asHost(), scope(t, "agent:ops")); err != nil {
		t.Fatalf("Release = %v, want the freeze released", err)
	}
	if active, _ := e.society.Freezer().State(asHost()); len(active) != 0 {
		t.Errorf("active freezes = %v, want none", active)
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
