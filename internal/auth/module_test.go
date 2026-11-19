// SPDX-License-Identifier: MIT

package auth_test

import (
	"testing"

	"github.com/SDK-E/agentsone/internal/auth"
	"github.com/SDK-E/agentsone/internal/core/coretest"
	"github.com/SDK-E/agentsone/internal/core/moduletest"
)

func TestModuleOwnsItsTags(t *testing.T) {
	deps, _ := coretest.NewDeps(t)
	m := auth.New(deps)
	if m.Name() != "auth" {
		t.Fatalf("name %q", m.Name())
	}
	moduletest.AssertOwnsTags(t, m, "session", "humans", "setup")
	moduletest.AssertEveryTagAnswersNotImplemented(t, m)
}
