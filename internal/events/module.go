// SPDX-License-Identifier: MIT

package events

import (
	"context"
	"crypto/rand"
	"fmt"

	"github.com/SDK-E/agentsone/emitters/clock"
	"github.com/SDK-E/agentsone/emitters/disk"
	"github.com/SDK-E/agentsone/internal/core"
	emitapp "github.com/SDK-E/agentsone/internal/emitkit/app"
	"github.com/SDK-E/agentsone/internal/events/adapters/builtin"
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
	// runner runs the built-in sources; nil when New failed.
	runner *builtin.Runner
	err    error
}

var _ core.Module = (*Module)(nil)

// Option changes how New builds the module.
type Option func(*options)

type options struct {
	sleeper emitapp.Sleeper
}

// WithSleeper makes the built-in sources wait with s instead of a real
// timer, so a test with a fake clock decides when they wake.
func WithSleeper(s emitapp.Sleeper) Option { return func(o *options) { o.sleeper = s } }

// New builds the events module on deps.Stores.Bus. The module needs no
// systemd: process emitters stay unavailable until a unit controller is
// supplied.
func New(deps core.Deps, opts ...Option) *Module {
	o := options{sleeper: emitapp.Timer{}}
	for _, opt := range opts {
		opt(&o)
	}
	m := &Module{db: deps.Stores.Bus, clock: deps.Clock}
	cat, err := catalog.Builtin()
	if err != nil {
		m.err = fmt.Errorf("load the event catalog: %w", err)
		return m
	}
	settings, err := builtin.LoadOptions(deps.Config)
	if err != nil {
		m.err = err
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
	m.runner = builtin.NewRunner(deps.Log, deps.Clock, o.sleeper, m.svc, sources(settings))
	return m
}

// sources lists the built-in sources that run inside the daemon. Each one is
// a Go package under emitters/ and publishes as its own built-in emitter.
func sources(o builtin.Options) []builtin.Named {
	d := o.Disk
	cfg := disk.Config{ThresholdPercent: d.ThresholdPercent, MarginPercent: d.MarginPercent, Interval: d.Interval.Duration}
	return []builtin.Named{
		{Name: "clock", Source: clock.New()},
		{Name: "disk", Source: disk.New(cfg, disk.MountsFile(d.MountsFile), disk.StatFS{})},
	}
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

// Start stores the built-in emitters that are not stored yet, then starts the
// built-in sources.
func (m *Module) Start(ctx context.Context) error {
	if m.err != nil {
		return m.err
	}
	if err := m.svc.SeedBuiltins(ctx); err != nil {
		return err
	}
	m.runner.Start(ctx)
	return nil
}

// Stop ends the built-in sources and waits for them.
func (m *Module) Stop(context.Context) error {
	if m.runner != nil {
		m.runner.Stop()
	}
	return nil
}

// API serves the events and triggers tags.
func (m *Module) API() core.API {
	if m.svc == nil {
		return core.API{}
	}
	return core.API{Groups: httpadapter.Groups(m.svc, m.clock)}
}
