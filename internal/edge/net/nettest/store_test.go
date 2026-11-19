// SPDX-License-Identifier: MIT

package nettest_test

import (
	"testing"

	"github.com/SDK-E/agentsone/internal/edge/net/app"
	"github.com/SDK-E/agentsone/internal/edge/net/nettest"
)

func TestMemoryStore(t *testing.T) {
	t.Parallel()
	nettest.RunAllowListStoreContract(t, func(*testing.T) app.AllowListStore { return nettest.NewStore() })
}

func TestMemoryFreezeStore(t *testing.T) {
	t.Parallel()
	nettest.RunFreezeStoreContract(t, func(*testing.T) app.FreezeStore { return nettest.NewFreezeStore() })
}
