// SPDX-License-Identifier: MIT

package auth_test

import (
	"context"
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
