// SPDX-License-Identifier: MIT

package agents

import (
	"github.com/SDK-E/agentsone/internal/contract/api/agentsapi"
	"github.com/SDK-E/agentsone/internal/contract/api/budgetapi"
	"github.com/SDK-E/agentsone/internal/contract/api/chartapi"
	"github.com/SDK-E/agentsone/internal/contract/api/charterapi"
	"github.com/SDK-E/agentsone/internal/core"
)

// Module wires the society context into the daemon.
type Module struct{ core.Base }

var _ core.Module = (*Module)(nil)

// New builds the agents module.
func New(core.Deps) *Module { return &Module{} }

// Name is the module and migration namespace.
func (*Module) Name() string { return "agents" }

// API serves the agents, chart, charter and budget tags. Every operation
// answers 501 until work package 1 lands.
func (*Module) API() core.API {
	return core.API{Groups: []core.APIGroup{
		agentsapi.Group(agentsapi.Unimplemented{}),
		chartapi.Group(chartapi.Unimplemented{}),
		charterapi.Group(charterapi.Unimplemented{}),
		budgetapi.Group(budgetapi.Unimplemented{}),
	}}
}
