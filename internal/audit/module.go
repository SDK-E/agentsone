// SPDX-License-Identifier: MIT

package audit

import (
	"context"
	"fmt"
	"net/url"
	"sync"

	"github.com/SDK-E/agentsone/internal/audit/adapters/edgeship"
	httpadapter "github.com/SDK-E/agentsone/internal/audit/adapters/http"
	"github.com/SDK-E/agentsone/internal/audit/adapters/sqlite"
	"github.com/SDK-E/agentsone/internal/audit/app"
	"github.com/SDK-E/agentsone/internal/contract/api/auditapi"
	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/store"
)

// Config is the [audit] table of the daemon configuration.
type Config struct {
	// EdgeSink is the edge audit sink's base URL, such as http://10.77.42.2:7710. When set, every
	// record is shipped there as written. Empty ships nothing (development, tests). The machine
	// image's entrypoint sets it from AGENTSONE_EDGE_IP.
	EdgeSink string `toml:"edge_sink"`
}

// Module wires the audit context into the daemon.
type Module struct {
	core.Base
	db      *store.DB
	log     *app.Log
	cfgErr  error
	shipper *edgeship.Shipper
	stop    context.CancelFunc
	wg      sync.WaitGroup
}

var _ core.Module = (*Module)(nil)

// New builds the audit module on deps.Stores.Audit.
func New(deps core.Deps) *Module {
	db := deps.Stores.Audit
	m := &Module{db: db, log: app.NewLog(sqlite.New(db), deps.Clock)}
	var cfg Config
	if deps.Config != nil {
		m.cfgErr = deps.Config.Section("audit", &cfg)
	}
	if m.cfgErr == nil && cfg.EdgeSink != "" {
		if u, err := url.Parse(cfg.EdgeSink); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			m.cfgErr = fmt.Errorf("[audit] edge_sink %q must be an http URL with a host", cfg.EdgeSink)
		} else {
			m.shipper = edgeship.New(edgeship.Options{
				Sink: cfg.EdgeSink, Source: m.log, Encode: app.EncodeRecord, Log: deps.Log,
			})
		}
	}
	return m
}

// Name is the module and migration namespace.
func (*Module) Name() string { return "audit" }

// Provide makes the log the daemon's Audit port.
func (m *Module) Provide(p *core.Ports) { p.Audit = m.log }

// Migrate creates the audit tables.
func (m *Module) Migrate(ctx context.Context) error { return sqlite.Migrate(ctx, m.db) }

// API serves the audit tag.
func (m *Module) API() core.API {
	return core.API{Groups: []core.APIGroup{auditapi.Group(httpadapter.New(m.log))}}
}

// Start begins shipping records to the edge when [audit] edge_sink is set. Shipping runs in the
// background and never holds up an append.
func (m *Module) Start(ctx context.Context) error {
	if m.cfgErr != nil {
		return m.cfgErr
	}
	if m.shipper == nil {
		return nil
	}
	ctx, m.stop = context.WithCancel(context.WithoutCancel(ctx))
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		m.shipper.Run(ctx)
	}()
	return nil
}

// Stop ends shipping. Records not shipped yet stay in audit.db and go at the next start.
func (m *Module) Stop(context.Context) error {
	if m.stop != nil {
		m.stop()
		m.wg.Wait()
	}
	return nil
}
