// SPDX-License-Identifier: MIT

package mcp

import (
	"github.com/SDK-E/agentsone/internal/contract/api/mcpapi"
	"github.com/SDK-E/agentsone/internal/core"
)

// Module wires the machine MCP into the daemon.
type Module struct{ core.Base }

var _ core.Module = (*Module)(nil)

// New builds the mcp module.
func New(core.Deps) *Module { return &Module{} }

// Name is the module and migration namespace.
func (*Module) Name() string { return "mcp" }

// API serves the mcp tag. Every operation answers 501 until work package 8 lands.
func (*Module) API() core.API {
	return core.API{Groups: []core.APIGroup{mcpapi.Group(mcpapi.Unimplemented{})}}
}
