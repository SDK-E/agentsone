// SPDX-License-Identifier: MIT

package sched_test

import (
	"testing"

	"github.com/SDK-E/agentsone/internal/core/coretest"
	"github.com/SDK-E/agentsone/internal/core/moduletest"
	"github.com/SDK-E/agentsone/internal/sched"
)

func TestModuleOwnsItsTags(t *testing.T) {
	deps, _ := coretest.NewDeps(t)
	m := sched.New(deps)
	if m.Name() != "sched" {
		t.Fatalf("name %q", m.Name())
	}
	moduletest.AssertOwnsTags(t, m, "tasks", "schedules")
	moduletest.AssertEveryTagAnswersNotImplemented(t, m)
}
