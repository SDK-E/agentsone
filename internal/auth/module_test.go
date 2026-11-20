// SPDX-License-Identifier: MIT

package auth_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/auth"
	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/core/coretest"
	"github.com/SDK-E/agentsone/internal/core/moduletest"
)

func TestModuleOwnsItsTags(t *testing.T) {
	t.Parallel()
	deps, _ := coretest.NewDeps(t)
	m := auth.New(deps)
	if m.Name() != "auth" {
		t.Fatalf("name %q", m.Name())
	}
	moduletest.AssertOwnsTags(t, m, "session", "humans", "setup")
}

func TestModule_ProvidesTheTokenIssuer(t *testing.T) {
	t.Parallel()
	deps, _ := coretest.NewDeps(t)
	m := auth.New(deps)
	m.Provide(deps.Ports)
	if err := m.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	tok, err := deps.Ports.Tokens.Issue(context.Background(), core.Caller{Kind: core.CallerAgent, Name: "founder"}, []string{"mcp:write"}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	c, err := deps.Ports.Tokens.Verify(context.Background(), tok.Value)
	if err != nil || c.Name != "founder" {
		t.Fatalf("caller %+v err %v", c, err)
	}
}

func TestModule_ProvidesTheHumanDirectory(t *testing.T) {
	t.Parallel()
	deps, _ := coretest.NewDeps(t)
	m := auth.New(deps)
	m.Provide(deps.Ports)
	ctx := context.Background()
	if err := m.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	all, err := deps.Ports.Humans.HumanAccounts(ctx)
	if err != nil || len(all) != 0 {
		t.Fatalf("humans before setup: %+v %v", all, err)
	}
}

func TestModule_ProvidesEngineSetup(t *testing.T) {
	t.Parallel()
	deps, _ := coretest.NewDeps(t)
	m := auth.New(deps)
	m.Provide(deps.Ports)
	ctx := context.Background()
	if err := m.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	// No licence file is installed in the test lib dir, so acceptance
	// cannot be checked yet.
	if _, err := deps.Ports.EngineSetup.LicenceAccepted(ctx); !errors.Is(err, core.ErrUnavailable) {
		t.Fatalf("licence: %v", err)
	}
	if err := deps.Ports.EngineSetup.SaveFetchStatus(ctx, core.EngineFetchStatus{State: "queued"}); err != nil {
		t.Fatalf("save: %v", err)
	}
}
