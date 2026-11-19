// SPDX-License-Identifier: MIT

package bus

import (
	"context"

	httpadapter "github.com/SDK-E/agentsone/internal/bus/adapters/http"
	"github.com/SDK-E/agentsone/internal/bus/adapters/sqlite"
	"github.com/SDK-E/agentsone/internal/bus/app"
	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/store"
)

// Module wires the bus context into the daemon.
type Module struct {
	core.Base
	db    *store.DB
	clock core.Clock
	svc   *app.Service
}

var _ core.Module = (*Module)(nil)

// New builds the bus module on deps.Stores.Bus.
func New(deps core.Deps) *Module {
	return &Module{
		db:    deps.Stores.Bus,
		clock: deps.Clock,
		svc: app.New(app.Deps{
			Channels: sqlite.NewChannels(deps.Stores.Bus),
			Messages: sqlite.NewMessages(deps.Stores.Bus),
			Clock:    deps.Clock,
			IDs:      deps.IDs,
			Ports:    deps.Ports,
			Log:      deps.Log,
		}),
	}
}

// Name is the module and migration namespace.
func (*Module) Name() string { return "bus" }

// Provide makes the service the daemon's bus sender, which the runner
// protocol's bus.send uses.
func (m *Module) Provide(p *core.Ports) { p.Bus = m.svc }

// Migrate creates the bus tables in bus.db.
func (m *Module) Migrate(ctx context.Context) error { return sqlite.Migrate(ctx, m.db) }

// API serves the bus tag.
func (m *Module) API() core.API {
	return core.API{Groups: []core.APIGroup{httpadapter.Group(m.svc, m.clock)}}
}
