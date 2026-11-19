// SPDX-License-Identifier: MIT

package moduletest_test

import (
	"testing"

	"github.com/SDK-E/agentsone/internal/contract/api/auditapi"
	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/core/moduletest"
)

type stubModule struct{ core.Base }

func (stubModule) Name() string { return "stub" }

func (stubModule) API() core.API {
	return core.API{Groups: []core.APIGroup{auditapi.Group(auditapi.Unimplemented{})}}
}

func TestOwnedTags(t *testing.T) {
	if got := moduletest.OwnedTags(stubModule{}); len(got) != 1 || got[0] != "audit" {
		t.Fatalf("owned tags %v", got)
	}
}

func TestAnswersNotImplemented(t *testing.T) {
	moduletest.AssertEveryTagAnswersNotImplemented(t, stubModule{})
}
