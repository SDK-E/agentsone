// SPDX-License-Identifier: MIT

package auth

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"log/slog"
	"path/filepath"

	httpadapter "github.com/SDK-E/agentsone/internal/auth/adapters/http"
	"github.com/SDK-E/agentsone/internal/auth/adapters/licencefile"
	"github.com/SDK-E/agentsone/internal/auth/adapters/sqlite"
	"github.com/SDK-E/agentsone/internal/auth/app"
	"github.com/SDK-E/agentsone/internal/auth/domain"
	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/store"
)

// Settings is the [auth] table of config.toml. Defaults follow RFC 9106.
type Settings struct {
	Argon2TimeCost  uint32 `toml:"argon2_time_cost"`
	Argon2MemoryKiB uint32 `toml:"argon2_memory_kib"`
	Argon2Threads   uint8  `toml:"argon2_threads"`
}

// Module wires the identity context into the daemon.
type Module struct {
	core.Base
	db    *store.DB
	repo  *sqlite.Repository
	svc   *app.Service
	ports *core.Ports
	log   *slog.Logger
}

var _ core.Module = (*Module)(nil)

// New builds the auth module on deps.Stores.Core. It panics only when the
// system random source fails, which leaves nothing safe to run.
func New(deps core.Deps) *Module {
	settings := Settings{
		Argon2TimeCost: domain.DefaultArgon2.Time, Argon2MemoryKiB: domain.DefaultArgon2.Memory, Argon2Threads: domain.DefaultArgon2.Threads,
	}
	if err := deps.Config.Section("auth", &settings); err != nil {
		panic(fmt.Sprintf("auth: %v", err))
	}
	db := deps.Stores.Core
	repo := sqlite.New(db)
	svc, err := app.New(app.Deps{
		Clock: deps.Clock, Rand: rand.Reader,
		Argon2:   domain.Argon2Params{Time: settings.Argon2TimeCost, Memory: settings.Argon2MemoryKiB, Threads: settings.Argon2Threads},
		Sessions: repo, Tokens: repo, Humans: repo, Setup: repo,
		Licence: licencefile.New(filepath.Join(deps.Config.LibDir, "engine", "licence.json")),
		Events:  lateEvents{deps.Ports}, Audit: lateAudit{deps.Ports}, Seed: lateSeed{deps.Ports}, Log: deps.Log,
	})
	if err != nil {
		panic(fmt.Sprintf("auth: %v", err))
	}
	log := deps.Log
	if log == nil {
		log = slog.Default()
	}
	return &Module{db: db, repo: repo, svc: svc, ports: deps.Ports, log: log}
}

// Name is the module and migration namespace.
func (*Module) Name() string { return "auth" }

// Provide makes the service the daemon's token issuer, its engine setup port
// and its human directory. The token issuer also verifies session cookies,
// so the API layer needs only that port.
func (m *Module) Provide(p *core.Ports) {
	p.Tokens = m.svc
	p.EngineSetup = m.svc
	p.Humans = m.svc
}

// Migrate creates the identity tables in core.db.
func (m *Module) Migrate(ctx context.Context) error { return sqlite.Migrate(ctx, m.db) }

// legacyRevokeDetail is the audit detail of a token migration 0002 revoked.
var legacyRevokeDetail = json.RawMessage(`{"reason":"earlier token id form, revoked at upgrade"}`)

// Start writes one audit record for each token that migration 0002
// revoked, naming it by id only, then forgets it. The audit log is not open
// while migrations run. A record that cannot be written is tried again at
// the next start; the token stays revoked either way.
func (m *Module) Start(ctx context.Context) error {
	ids, err := m.repo.LegacyRevokes(ctx)
	if err != nil {
		return fmt.Errorf("auth: %w", err)
	}
	ctx = core.WithCaller(ctx, core.Caller{Kind: core.CallerSystem, Name: "auth", UID: -1})
	for _, id := range ids {
		if err := m.ports.Audit.Append(ctx, core.AuditEntry{
			Actor: "system:auth", Action: "token.revoke", Target: "token:" + id, Detail: legacyRevokeDetail,
		}); err != nil {
			m.log.Error("auth: revoke of an earlier-form token not audited, trying again at the next start", "token", id, "err", err)
			return nil
		}
		if err := m.repo.LegacyRevokeAudited(ctx, id); err != nil {
			return fmt.Errorf("auth: %w", err)
		}
	}
	return nil
}

// API serves the session, humans and setup tags.
func (m *Module) API() core.API {
	return core.API{Groups: httpadapter.Groups(m.svc, lateAgents{m.ports})}
}

// The ports below are read on each call: New runs before other modules
// fill Ports in Provide.

type lateEvents struct{ p *core.Ports }

func (l lateEvents) Publish(ctx context.Context, e core.Event) (core.Event, error) {
	return l.p.Events.Publish(ctx, e) //nolint:wrapcheck // pass-through to the events port
}

type lateAudit struct{ p *core.Ports }

func (l lateAudit) Append(ctx context.Context, e core.AuditEntry) error {
	return l.p.Audit.Append(ctx, e) //nolint:wrapcheck // pass-through to the audit port
}

type lateSeed struct{ p *core.Ports }

func (l lateSeed) SeedInstalled(ctx context.Context) (bool, error) {
	return l.p.Seed.SeedInstalled(ctx) //nolint:wrapcheck // pass-through to the seed port
}

type lateAgents struct{ p *core.Ports }

func (l lateAgents) GetAgent(ctx context.Context, name string) (core.Agent, error) {
	return l.p.Agents.GetAgent(ctx, name) //nolint:wrapcheck // pass-through
}
