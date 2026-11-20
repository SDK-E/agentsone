// SPDX-License-Identifier: MIT

package sched

import (
	"context"
	"time"

	httpadapter "github.com/SDK-E/agentsone/internal/sched/adapters/http"
	"github.com/SDK-E/agentsone/internal/sched/adapters/sqlite"
	"github.com/SDK-E/agentsone/internal/sched/adapters/ticker"
	"github.com/SDK-E/agentsone/internal/sched/app"

	"github.com/SDK-E/agentsone/internal/contract/api/schedulesapi"
	"github.com/SDK-E/agentsone/internal/contract/api/tasksapi"
	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/store"
)

// Module wires the work context into the daemon.
type Module struct {
	core.Base
	db        *store.DB
	clock     core.Clock
	tasks     *app.Tasks
	schedules *app.Schedules
	ticker    *ticker.Ticker
}

var _ core.Module = (*Module)(nil)

// Option configures the module.
type Option func(*options)

type options struct{ beat <-chan time.Time }

// WithBeat looks for due timers on every value from beat instead of every
// ticker.DefaultInterval, so a test with a fake clock decides when the
// schedule clock checks.
func WithBeat(beat <-chan time.Time) Option { return func(o *options) { o.beat = beat } }

// New builds the sched module on deps.Stores.Core.
func New(deps core.Deps, opts ...Option) *Module {
	var o options
	for _, opt := range opts {
		opt(&o)
	}
	db := deps.Stores.Core
	d := app.Deps{Clock: deps.Clock, IDs: deps.IDs, Ports: deps.Ports, Log: deps.Log}
	schedules := app.NewSchedules(d, sqlite.NewSchedules(db))
	tick := ticker.New(schedules, deps.Log, ticker.DefaultInterval)
	if o.beat != nil {
		tick = ticker.NewWithBeat(schedules, deps.Log, o.beat)
	}
	return &Module{
		db: db, clock: deps.Clock,
		tasks:     app.NewTasks(d, sqlite.NewTasks(db)),
		schedules: schedules,
		ticker:    tick,
	}
}

// Name is the module and migration namespace.
func (*Module) Name() string { return "sched" }

// Provide makes the task service the daemon's Tasks port.
func (m *Module) Provide(p *core.Ports) { p.Tasks = m.tasks }

// Migrate creates the task and schedule tables.
func (m *Module) Migrate(ctx context.Context) error { return sqlite.Migrate(ctx, m.db) }

// API serves the tasks and schedules tags.
func (m *Module) API() core.API {
	return core.API{Groups: []core.APIGroup{
		tasksapi.Group(httpadapter.NewTasks(m.tasks, m.clock)),
		schedulesapi.Group(httpadapter.NewSchedules(m.schedules)),
	}}
}

// Start checks for due timers at once, so runs missed while the daemon was
// down fire after a restart, and then keeps checking.
func (m *Module) Start(ctx context.Context) error {
	m.ticker.Start(ctx)
	return nil
}

// Stop ends the timer loop.
func (m *Module) Stop(ctx context.Context) error { return m.ticker.Stop(ctx) }
