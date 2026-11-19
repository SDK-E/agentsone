// SPDX-License-Identifier: MIT

package api

import (
	"github.com/SDK-E/agentsone/internal/contract/api/hostapi"
	"github.com/SDK-E/agentsone/internal/core"
)

// Module wires the host door group into the daemon.
type Module struct{ core.Base }

var _ core.Module = (*Module)(nil)

// New builds the api module.
func New(core.Deps) *Module { return &Module{} }

// Name is the module and migration namespace.
func (*Module) Name() string { return "api" }

// API serves the host tag. Every operation answers 501 until work package 2 lands.
func (*Module) API() core.API {
	return core.API{Groups: []core.APIGroup{hostapi.Group(hostapi.Unimplemented{})}}
}
