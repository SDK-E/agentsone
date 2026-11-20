// SPDX-License-Identifier: MIT

package coretest

import (
	"context"
	"fmt"

	"github.com/SDK-E/agentsone/internal/core"
)

// Cells is an in-memory core.Cells: an agent's cell is
// /sys/fs/cgroup/agents/<name>, a human's /sys/fs/cgroup/humans/<name>,
// unless Err is set.
type Cells struct {
	// Err, when set, is every lookup's answer.
	Err error
}

var _ core.Cells = Cells{}

// AgentCell returns the agent's cgroup, or Err.
func (c Cells) AgentCell(_ context.Context, name string) (core.ProcessCell, error) {
	if c.Err != nil {
		return core.ProcessCell{}, fmt.Errorf("cell of %s: %w", name, c.Err)
	}
	return core.ProcessCell{Cgroup: "/sys/fs/cgroup/agents/" + name}, nil
}

// HumanCell returns the human's cgroup, or Err.
func (c Cells) HumanCell(_ context.Context, name string) (core.ProcessCell, error) {
	if c.Err != nil {
		return core.ProcessCell{}, fmt.Errorf("cell of human %s: %w", name, c.Err)
	}
	return core.ProcessCell{Cgroup: "/sys/fs/cgroup/humans/" + name}, nil
}
