// SPDX-License-Identifier: MIT

package events_test

import (
	"testing"

	"github.com/SDK-E/agentsone/internal/core/coretest"
	"github.com/SDK-E/agentsone/internal/core/moduletest"
	"github.com/SDK-E/agentsone/internal/events"
)

func TestModuleOwnsItsTags(t *testing.T) {
	deps, _ := coretest.NewDeps(t)
	m := events.New(deps)
	if m.Name() != "events" {
		t.Fatalf("name %q", m.Name())
	}
	moduletest.AssertOwnsTags(t, m, "events", "triggers")
	moduletest.AssertEveryTagAnswersNotImplemented(t, m)
}
