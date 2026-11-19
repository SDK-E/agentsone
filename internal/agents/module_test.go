// SPDX-License-Identifier: MIT

package agents_test

import (
	"testing"

	"github.com/SDK-E/agentsone/internal/agents"
	"github.com/SDK-E/agentsone/internal/core/coretest"
	"github.com/SDK-E/agentsone/internal/core/moduletest"
)

func TestModuleOwnsItsTags(t *testing.T) {
	deps, _ := coretest.NewDeps(t)
	m := agents.New(deps)
	if m.Name() != "agents" {
		t.Fatalf("name %q", m.Name())
	}
	moduletest.AssertOwnsTags(t, m, "agents", "chart", "charter", "budget")
	moduletest.AssertEveryTagAnswersNotImplemented(t, m)
}
