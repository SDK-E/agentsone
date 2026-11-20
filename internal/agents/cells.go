// SPDX-License-Identifier: MIT

package agents

import (
	"context"

	"github.com/SDK-E/agentsone/internal/agents/supervisor/adapters/process"
	"github.com/SDK-E/agentsone/internal/core"
)

// cells is the Cells port over the runner launcher, which holds the cgroup
// tree and every agent's limits.
type cells struct{ m *Module }

var _ core.Cells = cells{}

// launcher is the process launcher, or nil when a test injected its own
// launcher or processes: then runners run unplaced, and so do terminals.
func (c cells) launcher() *process.Launcher {
	if c.m.parts == nil {
		return nil
	}
	return c.m.parts.procs
}

func (c cells) AgentCell(ctx context.Context, name string) (core.ProcessCell, error) {
	if l := c.launcher(); l != nil {
		return l.AgentCell(ctx, name)
	}
	return core.ProcessCell{}, nil
}

func (c cells) HumanCell(ctx context.Context, name string) (core.ProcessCell, error) {
	if l := c.launcher(); l != nil {
		return l.HumanCell(ctx, name)
	}
	return core.ProcessCell{}, nil
}
