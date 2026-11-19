// SPDX-License-Identifier: MIT

package memory_test

import (
	"testing"

	"github.com/SDK-E/agentsone/internal/router/adapters/memory"
	"github.com/SDK-E/agentsone/internal/router/domain"
	"github.com/SDK-E/agentsone/internal/router/routertest"
)

func TestLinesContract(t *testing.T) {
	routertest.RunLineContract(t, func(*testing.T) domain.LineRepository { return memory.NewLines() })
}

func TestAssignmentsContract(t *testing.T) {
	routertest.RunAssignmentContract(t, func(*testing.T) domain.AssignmentRepository { return memory.NewAssignments() })
}
