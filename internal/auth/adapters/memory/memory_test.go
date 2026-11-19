// SPDX-License-Identifier: MIT

package memory_test

import (
	"testing"

	"github.com/SDK-E/agentsone/internal/auth/adapters/memory"
	"github.com/SDK-E/agentsone/internal/auth/authtest"
)

func TestStore_MeetsTheContract(t *testing.T) {
	t.Parallel()
	authtest.RunRepositoryContract(t, func(*testing.T) authtest.Repo { return memory.New() })
}
