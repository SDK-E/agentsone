// SPDX-License-Identifier: MIT

package mcp_test

import (
	"testing"

	"github.com/SDK-E/agentsone/internal/core/coretest"
	"github.com/SDK-E/agentsone/internal/core/moduletest"
	"github.com/SDK-E/agentsone/internal/mcp"
)

func TestModuleOwnsItsTags(t *testing.T) {
	deps, _ := coretest.NewDeps(t)
	m := mcp.New(deps)
	if m.Name() != "mcp" {
		t.Fatalf("name %q", m.Name())
	}
	moduletest.AssertOwnsTags(t, m, "mcp")
	moduletest.AssertEveryTagAnswersNotImplemented(t, m)
}
