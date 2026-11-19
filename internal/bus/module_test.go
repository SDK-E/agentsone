// SPDX-License-Identifier: MIT

package bus_test

import (
	"testing"

	"github.com/SDK-E/agentsone/internal/bus"
	"github.com/SDK-E/agentsone/internal/core/coretest"
	"github.com/SDK-E/agentsone/internal/core/moduletest"
)

func TestModuleOwnsItsTags(t *testing.T) {
	deps, _ := coretest.NewDeps(t)
	m := bus.New(deps)
	if m.Name() != "bus" {
		t.Fatalf("name %q", m.Name())
	}
	moduletest.AssertOwnsTags(t, m, "bus")
	moduletest.AssertEveryTagAnswersNotImplemented(t, m)
}
