// SPDX-License-Identifier: MIT

package audit

import (
	"context"

	httpadapter "github.com/SDK-E/agentsone/internal/audit/adapters/http"
	"github.com/SDK-E/agentsone/internal/audit/adapters/sqlite"
	"github.com/SDK-E/agentsone/internal/audit/app"
	"github.com/SDK-E/agentsone/internal/contract/api/auditapi"
	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/store"
)

// Module wires the audit context into the daemon.
type Module struct {
	core.Base
	db  *store.DB
	log *app.Log
}

var _ core.Module = (*Module)(nil)

// New builds the audit module on deps.Stores.Audit.
func New(deps core.Deps) *Module {
	db := deps.Stores.Audit
	return &Module{db: db, log: app.NewLog(sqlite.New(db), deps.Clock)}
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
