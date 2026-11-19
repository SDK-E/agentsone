// SPDX-License-Identifier: MIT

package plugins

import (
	"github.com/SDK-E/agentsone/internal/contract/api/pluginsapi"
	"github.com/SDK-E/agentsone/internal/contract/api/runnersapi"
	"github.com/SDK-E/agentsone/internal/contract/api/themesapi"
	"github.com/SDK-E/agentsone/internal/core"
)

// Module wires the plugin context into the daemon.
type Module struct{ core.Base }

var _ core.Module = (*Module)(nil)

// New builds the plugins module.
func New(core.Deps) *Module { return &Module{} }

// Name is the module and migration namespace.
func (*Module) Name() string { return "plugins" }

// API serves the plugins, runners and themes tags. Every operation answers
// 501 until work package 6 lands.
func (*Module) API() core.API {
	return core.API{Groups: []core.APIGroup{
		pluginsapi.Group(pluginsapi.Unimplemented{}),
		runnersapi.Group(runnersapi.Unimplemented{}),
		themesapi.Group(themesapi.Unimplemented{}),
	}}
}
