// SPDX-License-Identifier: MIT

package memory

import (
	"github.com/SDK-E/agentsone/internal/contract/api/memoryapi"
	"github.com/SDK-E/agentsone/internal/core"
)

// Module wires the memory context into the daemon.
type Module struct{ core.Base }

var _ core.Module = (*Module)(nil)

// New builds the memory module.
func New(core.Deps) *Module { return &Module{} }

// Name is the module and migration namespace.
func (*Module) Name() string { return "memory" }

// API serves the memory tag. Every operation answers 501 until work package 5 lands.
func (*Module) API() core.API {
	return core.API{Groups: []core.APIGroup{memoryapi.Group(memoryapi.Unimplemented{})}}
}
