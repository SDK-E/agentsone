// SPDX-License-Identifier: MIT

package memory_test

import (
	"testing"

	"github.com/SDK-E/agentsone/internal/plugins/adapters/memory"
	"github.com/SDK-E/agentsone/internal/plugins/domain"
	"github.com/SDK-E/agentsone/internal/plugins/pluginstest"
)

func TestPluginsContract(t *testing.T) {
	t.Parallel()
	pluginstest.RunPluginRepositoryContract(t, func(*testing.T) domain.PluginRepository {
		return memory.NewPlugins()
	})
}
