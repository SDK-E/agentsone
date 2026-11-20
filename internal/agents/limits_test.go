// SPDX-License-Identifier: MIT

package agents_test

import (
	"testing"

	"github.com/SDK-E/agentsone/internal/agents"
	"github.com/SDK-E/agentsone/internal/core/coretest"
)

// The daemon's own runner processes put custom agents' cgroups under the
// root the boot probe prepared, and use none when the probe found soft
// limits (an empty root).
func TestModule_RunnersUseTheCgroupRootFromTheBootProbe(t *testing.T) {
	t.Parallel()
	for _, root := range []string{"/sys/fs/cgroup/agents", ""} {
		deps, _ := coretest.NewDeps(t)
		m := agents.New(deps, agents.WithSupervisor(agents.SupervisorOptions{
			CgroupRoot: root, Accounts: &accounts{}, Wall: wall{holdingWall},
		}))
		if got := agents.RunnerCgroupRoot(m); got != root {
			t.Errorf("runner cgroup root = %q, want %q", got, root)
		}
	}
}
