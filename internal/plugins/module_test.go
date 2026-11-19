// SPDX-License-Identifier: MIT

package plugins_test

import (
	"testing"

	"github.com/SDK-E/agentsone/internal/core/coretest"
	"github.com/SDK-E/agentsone/internal/core/moduletest"
	"github.com/SDK-E/agentsone/internal/plugins"
)

func TestModuleOwnsItsTags(t *testing.T) {
	deps, _ := coretest.NewDeps(t)
	m := plugins.New(deps)
	if m.Name() != "plugins" {
		t.Fatalf("name %q", m.Name())
	}
	moduletest.AssertOwnsTags(t, m, "plugins", "runners", "themes")
	moduletest.AssertEveryTagAnswersNotImplemented(t, m)
}
