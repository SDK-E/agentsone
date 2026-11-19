// SPDX-License-Identifier: MIT

package machine

import (
	"github.com/SDK-E/agentsone/internal/contract/api/ideapi"
	"github.com/SDK-E/agentsone/internal/contract/api/machineapi"
	"github.com/SDK-E/agentsone/internal/contract/api/netapi"
	"github.com/SDK-E/agentsone/internal/core"
)

// Module wires machine control into the daemon.
type Module struct{ core.Base }

var _ core.Module = (*Module)(nil)

// New builds the machine module.
func New(core.Deps) *Module { return &Module{} }

// Name is the module and migration namespace.
func (*Module) Name() string { return "machine" }

// API serves the machine, net and ide tags. Every operation answers 501 until
// work packages 7 and 13 land.
func (*Module) API() core.API {
	return core.API{Groups: []core.APIGroup{
		machineapi.Group(machineapi.Unimplemented{}),
		netapi.Group(netapi.Unimplemented{}),
		ideapi.Group(ideapi.Unimplemented{}),
	}}
}
