// SPDX-License-Identifier: MIT

package api_test

import (
	"testing"

	"github.com/SDK-E/agentsone/internal/api"
	"github.com/SDK-E/agentsone/internal/core/coretest"
	"github.com/SDK-E/agentsone/internal/core/moduletest"
)

func TestModuleOwnsItsTags(t *testing.T) {
	deps, _ := coretest.NewDeps(t)
	m := api.New(deps)
	if m.Name() != "api" {
		t.Fatalf("name %q", m.Name())
	}
	moduletest.AssertOwnsTags(t, m, "host")
	moduletest.AssertEveryTagAnswersNotImplemented(t, m)
}
