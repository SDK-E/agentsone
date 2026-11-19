// SPDX-License-Identifier: MIT

package memory

import (
	"context"

	"github.com/SDK-E/agentsone/internal/contract/api/memoryapi"
	"github.com/SDK-E/agentsone/internal/core"
	httpadapter "github.com/SDK-E/agentsone/internal/memory/adapters/http"
	"github.com/SDK-E/agentsone/internal/memory/adapters/sqlite"
	"github.com/SDK-E/agentsone/internal/memory/app"
	"github.com/SDK-E/agentsone/internal/store"
)

// Module wires the memory context into the daemon.
type Module struct {
	core.Base
	db  *store.DB
	svc *app.Service
}

var _ core.Module = (*Module)(nil)

// New builds the memory module on deps.Stores.Memory.
func New(deps core.Deps) *Module {
	db := deps.Stores.Memory
	return &Module{db: db, svc: app.New(sqlite.New(db), deps.Ports, deps.Clock, deps.IDs)}
}

// Name is the module and migration namespace.
func (*Module) Name() string { return "memory" }

// Migrate creates the memory tables and the full-text index.
func (m *Module) Migrate(ctx context.Context) error { return sqlite.Migrate(ctx, m.db) }

// API serves the memory tag.
func (m *Module) API() core.API {
	return core.API{Groups: []core.APIGroup{memoryapi.Group(httpadapter.New(m.svc))}}
}
