// SPDX-License-Identifier: MIT

package agents

import "github.com/SDK-E/agentsone/internal/agents/supervisor/adapters/process"

// RunnerCgroupRoot is the cgroup root the daemon's own runner processes
// use, or "" when the module runs on an injected launcher.
func RunnerCgroupRoot(m *Module) string {
	if m.parts == nil || m.parts.wrapped == nil {
		return ""
	}
	if l, ok := m.parts.wrapped.Inner.(*process.Launcher); ok {
		return l.CgroupRoot
	}
	return ""
}
