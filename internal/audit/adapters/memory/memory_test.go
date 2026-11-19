// SPDX-License-Identifier: MIT

package memory_test

import (
	"testing"

	"github.com/SDK-E/agentsone/internal/audit/adapters/memory"
	"github.com/SDK-E/agentsone/internal/audit/audittest"
	"github.com/SDK-E/agentsone/internal/audit/domain"
)

func TestRepositoryContract(t *testing.T) {
	audittest.RunRepositoryContract(t, func(*testing.T) domain.Repository { return memory.New() })
}
