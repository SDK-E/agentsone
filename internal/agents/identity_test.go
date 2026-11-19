// SPDX-License-Identifier: MIT

package agents

import (
	"context"
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/SDK-E/agentsone/internal/agents/adapters/memory"
	"github.com/SDK-E/agentsone/internal/agents/agentstest"
	"github.com/SDK-E/agentsone/internal/agents/app"
	"github.com/SDK-E/agentsone/internal/agents/domain"
	supapp "github.com/SDK-E/agentsone/internal/agents/supervisor/app"
	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/core/coretest"
)

var _ supapp.Identity = identity{}

func identityWorld(t *testing.T) (identity, *memory.Repository, *coretest.Fakes, *app.Society) {
	t.Helper()
	fakes, ports := coretest.NewFakes()
	repo := memory.New()
	for _, a := range agentstest.Society() {
		if err := repo.CreateAgent(context.Background(), a); err != nil {
			t.Fatal(err)
		}
	}
	s := app.New(repo, agentstest.NewRuntime(), ports, fakes.Clock, fakes.IDs)
	return identity{s: s}, repo, fakes, s
}

// The welcome says who the agent is: its directory entry, team, the
// charter, its one-line and its open goals.
func TestIdentity_TellsTheRunnerWhoTheAgentIs(t *testing.T) {
	t.Parallel()
	id, repo, fakes, s := identityWorld(t)
	founder := core.WithCaller(context.Background(), core.Caller{Kind: core.CallerAgent, Name: "founder", IsRoot: true})
	if _, err := s.RewriteCharter(founder, 0, "We work for the humans here."); err != nil {
		t.Fatal(err)
	}
	if err := fakes.Live.UpdateLine(context.Background(), "dev", "Goal: ship. Now: testing."); err != nil {
		t.Fatal(err)
	}
	for _, g := range []domain.Goal{
		{ID: "gol_01", Owner: "dev", Title: "Ship the release", Status: domain.GoalOpen},
		{ID: "gol_02", Owner: "dev", Title: "Old work", Status: domain.GoalAchieved},
		{ID: "gol_03", Owner: "ops", Title: "Not dev's", Status: domain.GoalOpen},
	} {
		if err := repo.CreateGoal(context.Background(), g); err != nil {
			t.Fatal(err)
		}
	}

	got, err := id.Identity(context.Background(), "dev")
	if err != nil {
		t.Fatal(err)
	}

	dev, _ := repo.GetAgent(context.Background(), "dev")
	want := supapp.AgentIdentity{
		Agent: dev.Core(), Team: "lead", Charter: "We work for the humans here.",
		OneLine: "Goal: ship. Now: testing.", Goals: []string{"Ship the release"},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("Identity (-want +got):\n%s", diff)
	}
}

// A new agent without a one-line yet still gets a valid one.
func TestIdentity_GivesAOneLineBeforeTheAgentSetsOne(t *testing.T) {
	t.Parallel()
	id, _, _, _ := identityWorld(t)
	got, err := id.Identity(context.Background(), "dev")
	if err != nil || got.OneLine != startingOneLine || got.Goals == nil {
		t.Fatalf("Identity = %+v, %v; want the starting one-line and no goals", got, err)
	}
}

func TestIdentity_UnknownAgentIsNotFound(t *testing.T) {
	t.Parallel()
	id, _, _, _ := identityWorld(t)
	if _, err := id.Identity(context.Background(), "ghost"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("error = %v, want ErrNotFound", err)
	}
}

// A broken store is reported, never answered with a partial identity.
func TestIdentity_ReportsStorageFailures(t *testing.T) {
	t.Parallel()
	for _, method := range []string{"ListAgents", "Charter"} {
		t.Run(method, func(t *testing.T) {
			t.Parallel()
			fakes, ports := coretest.NewFakes()
			repo := memory.New()
			for _, a := range agentstest.Society() {
				_ = repo.CreateAgent(context.Background(), a)
			}
			broken := agentstest.Broken{Repository: repo, Err: errors.New("disk failed"), Fail: map[string]bool{method: true}}
			id := identity{s: app.New(broken, agentstest.NewRuntime(), ports, fakes.Clock, fakes.IDs)}
			if _, err := id.Identity(context.Background(), "dev"); err == nil {
				t.Fatal("no error")
			}
		})
	}
}
