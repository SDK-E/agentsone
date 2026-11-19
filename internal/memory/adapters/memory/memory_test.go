// SPDX-License-Identifier: MIT

package memory_test

import (
	"testing"

	"github.com/SDK-E/agentsone/internal/memory/adapters/memory"
	"github.com/SDK-E/agentsone/internal/memory/domain"
	"github.com/SDK-E/agentsone/internal/memory/memorytest"
)

func TestRepositoryContract(t *testing.T) {
	memorytest.RunRepositoryContract(t, func(*testing.T) domain.Repository { return memory.New() })
}
