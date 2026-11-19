// SPDX-License-Identifier: MIT

package events

import (
	"github.com/SDK-E/agentsone/internal/contract/api/eventsapi"
	"github.com/SDK-E/agentsone/internal/contract/api/triggersapi"
	"github.com/SDK-E/agentsone/internal/core"
)

// Module wires the events context into the daemon.
type Module struct{ core.Base }

var _ core.Module = (*Module)(nil)

// New builds the events module.
func New(core.Deps) *Module { return &Module{} }

// Name is the module and migration namespace.
func (*Module) Name() string { return "events" }

// API serves the events and triggers tags. Every operation answers 501 until
// work package 3 lands.
func (*Module) API() core.API {
	return core.API{Groups: []core.APIGroup{
		eventsapi.Group(eventsapi.Unimplemented{}),
		triggersapi.Group(triggersapi.Unimplemented{}),
	}}
}
