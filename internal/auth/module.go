// SPDX-License-Identifier: MIT

package auth

import (
	"github.com/SDK-E/agentsone/internal/contract/api/humansapi"
	"github.com/SDK-E/agentsone/internal/contract/api/sessionapi"
	"github.com/SDK-E/agentsone/internal/contract/api/setupapi"
	"github.com/SDK-E/agentsone/internal/core"
)

// Module wires the identity context into the daemon.
type Module struct{ core.Base }

var _ core.Module = (*Module)(nil)

// New builds the auth module.
func New(core.Deps) *Module { return &Module{} }

// Name is the module and migration namespace.
func (*Module) Name() string { return "auth" }

// API serves the session, humans and setup tags. Every operation answers 501
// until work package 2 lands.
func (*Module) API() core.API {
	return core.API{Groups: []core.APIGroup{
		sessionapi.Group(sessionapi.Unimplemented{}),
		humansapi.Group(humansapi.Unimplemented{}),
		setupapi.Group(setupapi.Unimplemented{}),
	}}
}
