// SPDX-License-Identifier: MIT

package events

import (
	"context"
	"crypto/rand"
	"fmt"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/events/adapters/catalog"
	httpadapter "github.com/SDK-E/agentsone/internal/events/adapters/http"
	"github.com/SDK-E/agentsone/internal/events/adapters/sqlite"
	"github.com/SDK-E/agentsone/internal/events/app"
	"github.com/SDK-E/agentsone/internal/store"
)

// Module wires the events context into the daemon.
type Module struct {
	core.Base
	db    *store.DB
	clock core.Clock
	svc   *app.Service
	err   error
}

var _ core.Module = (*Module)(nil)

// New builds the events module on deps.Stores.Bus. The module needs no
// systemd: process emitters stay unavailable until a unit controller is
// supplied.
func New(deps core.Deps) *Module {
	m := &Module{db: deps.Stores.Bus, clock: deps.Clock}
	cat, err := catalog.Builtin()
	if err != nil {
		m.err = fmt.Errorf("load the event catalog: %w", err)
		return m
	}
	m.svc = app.New(app.Deps{
		Events:   sqlite.NewEvents(m.db),
		Emitters: sqlite.NewEmitters(m.db),
		Triggers: sqlite.NewTriggers(m.db),
		Catalog:  cat,
		Units:    app.NoUnits{},
		Clock:    deps.Clock,
		IDs:      deps.IDs,
		Ports:    deps.Ports,
		Log:      deps.Log,
		Entropy:  rand.Reader,
	})
	return m
}

// Name is the module and migration namespace.
func (*Module) Name() string { return "events" }

// Provide makes the service the daemon's event publisher and subscriber.
func (m *Module) Provide(p *core.Ports) {
	if m.svc == nil {
		return
	}
	p.Events = m.svc
	p.Subscribe = m.svc
}

// Migrate creates the events tables in bus.db.
func (m *Module) Migrate(ctx context.Context) error {
	if m.err != nil {
		return m.err
	}
	return sqlite.Migrate(ctx, m.db)
}

// Start stores the built-in emitters that are not stored yet.
func (m *Module) Start(ctx context.Context) error {
	if m.err != nil {
		return m.err
	}
	return m.svc.SeedBuiltins(ctx)
}

// API serves the events and triggers tags.
func (m *Module) API() core.API {
	if m.svc == nil {
		return core.API{}
	}
	return core.API{Groups: httpadapter.Groups(m.svc, m.clock)}
}
