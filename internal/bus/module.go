// SPDX-License-Identifier: MIT

package bus

import (
	"github.com/SDK-E/agentsone/internal/contract/api/busapi"
	"github.com/SDK-E/agentsone/internal/core"
)

// Module wires the bus context into the daemon.
type Module struct{ core.Base }

var _ core.Module = (*Module)(nil)

// New builds the bus module.
func New(core.Deps) *Module { return &Module{} }

// Name is the module and migration namespace.
func (*Module) Name() string { return "bus" }

// API serves the bus tag. Every operation answers 501 until work package 3 lands.
func (*Module) API() core.API {
	return core.API{Groups: []core.APIGroup{busapi.Group(busapi.Unimplemented{})}}
}
