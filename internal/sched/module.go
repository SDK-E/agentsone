// SPDX-License-Identifier: MIT

package sched

import (
	"github.com/SDK-E/agentsone/internal/contract/api/schedulesapi"
	"github.com/SDK-E/agentsone/internal/contract/api/tasksapi"
	"github.com/SDK-E/agentsone/internal/core"
)

// Module wires the work context into the daemon.
type Module struct{ core.Base }

var _ core.Module = (*Module)(nil)

// New builds the sched module.
func New(core.Deps) *Module { return &Module{} }

// Name is the module and migration namespace.
func (*Module) Name() string { return "sched" }

// API serves the tasks and schedules tags. Every operation answers 501 until
// work package 3 lands.
func (*Module) API() core.API {
	return core.API{Groups: []core.APIGroup{
		tasksapi.Group(tasksapi.Unimplemented{}),
		schedulesapi.Group(schedulesapi.Unimplemented{}),
	}}
}
