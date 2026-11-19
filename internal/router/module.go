// SPDX-License-Identifier: MIT

package router

import (
	"github.com/SDK-E/agentsone/internal/contract/api/routerapi"
	"github.com/SDK-E/agentsone/internal/core"
)

// Module wires the router context into the daemon.
type Module struct{ core.Base }

var _ core.Module = (*Module)(nil)

// New builds the router module.
func New(core.Deps) *Module { return &Module{} }

// Name is the module and migration namespace.
func (*Module) Name() string { return "router" }

// API serves the router tag. Every operation answers 501 until work package 4 lands.
func (*Module) API() core.API {
	return core.API{Groups: []core.APIGroup{routerapi.Group(routerapi.Unimplemented{})}}
}
