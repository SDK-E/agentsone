// SPDX-License-Identifier: MIT

package agents_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/SDK-E/agentsone/internal/agents"
	"github.com/SDK-E/agentsone/internal/agents/agentstest"
	"github.com/SDK-E/agentsone/internal/agents/domain"
	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/core/coretest"
	"github.com/SDK-E/agentsone/internal/core/moduletest"
)

func TestModule_OwnsItsTags(t *testing.T) {
	t.Parallel()
	deps, _ := coretest.NewDeps(t)
	m := agents.New(deps)
	if m.Name() != "agents" {
		t.Fatalf("name %q", m.Name())
	}
	moduletest.AssertOwnsTags(t, m, "agents", "chart", "charter", "budget")
}

func TestModule_ProvidesItsPorts(t *testing.T) {
	t.Parallel()
	deps, _ := coretest.NewDeps(t)
	m := agents.New(deps, agents.WithRuntime(agentstest.NewRuntime()))
	p := core.NewPorts()
	ctx := context.Background()
	if err := m.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	m.Provide(p)

	if _, err := p.Agents.ListAgents(ctx); err != nil {
		t.Errorf("Agents port: %v", err)
	}
	if _, err := p.Freezer.State(ctx); err != nil {
		t.Errorf("Freezer port: %v", err)
	}
	if _, err := p.Budget.Allocation(ctx, "ghost"); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("Budget port: %v, want not found", err)
	}
	if err := p.Waker.Wake(ctx, "ghost", core.WakeEvent, nil); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("Waker port: %v, want not found", err)
	}
	if _, err := p.Profiles.Profiles(ctx); err != nil {
		t.Errorf("Profiles port: %v", err)
	}
	human := core.WithCaller(ctx, core.Caller{Kind: core.CallerHuman, Name: "ada", UID: -1})
	if _, err := p.Chat.PostChat(human, "ghost", "hi", ""); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("Chat port: %v, want not found", err)
	}
}

func TestModule_StartReprovisionsEveryActiveAgentWithItsUID(t *testing.T) {
	t.Parallel()
	deps, _ := coretest.NewDeps(t)
	rt := agentstest.NewRuntime()
	m := agents.New(deps, agents.WithRuntime(rt))
	ctx := core.WithCaller(context.Background(), core.Caller{Kind: core.CallerSystem, Name: "seed"})
	if err := m.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	founder := domain.HireSpec{Name: "founder", DisplayName: "Founder", Kind: domain.KindAlwaysOn, Runner: "echo", Role: "Founder"}
	if _, err := m.Society().Found(ctx, founder); err != nil {
		t.Fatal(err)
	}
	if err := m.Society().Freezer().Freeze(ctx, core.FreezeScope{Kind: core.FreezeAgent, Target: "founder"}); err != nil {
		t.Fatal(err)
	}
	restarted := agentstest.NewRuntime()
	again := agents.New(deps, agents.WithRuntime(restarted))

	if err := again.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	if diff := cmp.Diff([]string{"founder"}, restarted.Provisioned()); diff != "" {
		t.Errorf("reprovisioned (-want +got):\n%s", diff)
	}
	if !restarted.Frozen("agent:founder") {
		t.Error("freeze was not replayed on start")
	}
}

func TestModule_StartsWithoutARunnerSupervisor(t *testing.T) {
	t.Parallel()
	deps, _ := coretest.NewDeps(t)
	m := agents.New(deps)
	if err := m.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}

	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("Start() without a supervisor error = %v, want nil", err)
	}
}
