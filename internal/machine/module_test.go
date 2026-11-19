// SPDX-License-Identifier: MIT

package machine_test

import (
	"testing"

	"github.com/SDK-E/agentsone/internal/core/coretest"
	"github.com/SDK-E/agentsone/internal/core/moduletest"
	"github.com/SDK-E/agentsone/internal/machine"
)

func TestModuleOwnsItsTags(t *testing.T) {
	deps, _ := coretest.NewDeps(t)
	m := machine.New(deps)
	if m.Name() != "machine" {
		t.Fatalf("name %q", m.Name())
	}
	moduletest.AssertOwnsTags(t, m, "machine", "net", "ide")
	moduletest.AssertEveryTagAnswersNotImplemented(t, m)
}
