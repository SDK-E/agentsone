// SPDX-License-Identifier: MIT

package router

import (
	"context"
	"time"

	"github.com/SDK-E/agentsone/internal/contract/api/routerapi"
	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/router/adapters/gateway"
	httpadapter "github.com/SDK-E/agentsone/internal/router/adapters/http"
	"github.com/SDK-E/agentsone/internal/router/adapters/sqlite"
	"github.com/SDK-E/agentsone/internal/router/app"
	"github.com/SDK-E/agentsone/internal/store"
)

// config is the [router] table of the daemon config.
type config struct {
	// GatewayURL is the OpenAI-compatible edge gateway.
	GatewayURL string `toml:"gateway_url"`
	// TimeoutSeconds bounds the one quick model call.
	TimeoutSeconds int `toml:"timeout_seconds"`
}

func defaultConfig() config {
	return config{GatewayURL: "http://edge:4000", TimeoutSeconds: 20}
}

// Module wires the router context into the daemon.
type Module struct {
	core.Base
	db   *store.DB
	live *app.LiveList
	http *httpadapter.Handler
}

var _ core.Module = (*Module)(nil)

// New builds the router module on the core database.
func New(deps core.Deps) *Module {
	cfg := defaultConfig()
	if err := deps.Config.Section("router", &cfg); err != nil {
		deps.Log.Error("router config ignored, using defaults", "error", err)
		cfg = defaultConfig()
	}
	if cfg.TimeoutSeconds < 1 {
		cfg.TimeoutSeconds = defaultConfig().TimeoutSeconds
	}
	repo := sqlite.New(deps.Stores.Core)
	live := app.NewLiveList(repo, profileAdapter{ports: deps.Ports}, deps.Clock)
	routing := app.NewRouter(app.RouterDeps{
		Live: live, Chats: chatAdapter{ports: deps.Ports}, Credentials: credentialAdapter{ports: deps.Ports},
		Gateway:     gateway.New(cfg.GatewayURL, time.Duration(cfg.TimeoutSeconds)*time.Second),
		Assignments: repo, Clock: deps.Clock, IDs: deps.IDs, Log: deps.Log,
	})
	return &Module{db: deps.Stores.Core, live: live, http: httpadapter.New(live, routing, deps.Clock)}
}

// Name is the module and migration namespace.
func (*Module) Name() string { return "router" }

// Provide makes the live list the daemon's Live port.
func (m *Module) Provide(p *core.Ports) { p.Live = m.live }

// Migrate creates the router tables.
func (m *Module) Migrate(ctx context.Context) error { return sqlite.Migrate(ctx, m.db) }

// API serves the router tag.
func (m *Module) API() core.API {
	return core.API{Groups: []core.APIGroup{routerapi.Group(m.http)}}
}
