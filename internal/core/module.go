// SPDX-License-Identifier: MIT

package core

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/SDK-E/agentsone/internal/store"
)

// Module is one daemon package (agents, bus, memory, ...). cmd/agentsoned
// drives every module through the lifecycle described in the package doc.
type Module interface {
	// Name is the short, stable module name, used in logs and as the
	// migration namespace. Lowercase, no spaces.
	Name() string
	// Provide puts the ports this module owns into p. It must not read
	// other ports or block. Leave ports owned by other modules alone.
	Provide(p *Ports)
	// Migrate brings this module's tables up to date. It runs before Start.
	Migrate(ctx context.Context) error
	// API returns the OpenAPI groups and extra routes this module serves.
	API() API
	// Start begins background work. It must return promptly; long-running
	// loops belong in goroutines stopped by Stop.
	Start(ctx context.Context) error
	// Stop ends background work. It runs in reverse start order.
	Stop(ctx context.Context) error
}

// API is what a module hands to the API layer.
type API struct {
	// Groups are OpenAPI tags this module implements. Build each one with
	// the generated helper, for example agentsapi.Group(impl). A tag may be
	// served by one module only.
	Groups []APIGroup
	// Routes are extra handlers outside the OpenAPI document, such as a
	// WebSocket terminal. Patterns use net/http ServeMux syntax.
	Routes []Route
}

// APIGroup is one OpenAPI tag ready to mount. The generated helper in each
// internal/contract/api/<tag>api package builds it; modules do not fill it
// by hand.
type APIGroup struct {
	// Tag is the OpenAPI tag name exactly as written in the document.
	Tag string
	// Register mounts every operation of the tag on mux.
	Register func(mux *http.ServeMux, opts APIOptions)
}

// APIOptions are supplied by the API layer when it mounts a group.
type APIOptions struct {
	// RequestError writes a 400 problem for a request the generated code
	// could not decode (bad parameters or body).
	RequestError func(w http.ResponseWriter, r *http.Request, err error)
	// ResponseError writes the problem for an error a handler returned. It
	// maps errors with ProblemFrom.
	ResponseError func(w http.ResponseWriter, r *http.Request, err error)
}

// Route is one extra HTTP handler.
type Route struct {
	// Pattern is a net/http ServeMux pattern, for example "GET /v1/pty/{id}/ws".
	Pattern string
	Handler http.Handler
}

// Deps is passed to every module constructor.
type Deps struct {
	Config *Config
	Log    *slog.Logger
	Clock  Clock
	IDs    IDs
	Stores Stores
	// Ports is shared by all modules and filled during the Provide step.
	// Read fields when used, not in the constructor.
	Ports *Ports
}

// Stores are the daemon's SQLite databases. Each module uses only the
// database its work package owns and migrates it under its own Name.
type Stores struct {
	Core   *store.DB // agents, chart, tasks, schedules, plugins, sessions
	Bus    *store.DB // bus messages and events
	Memory *store.DB // memory records with full-text search
	Audit  *store.DB // hash-chained audit records
}

// Base gives a module no-op lifecycle methods so it only writes what it
// needs. Embed it and override.
type Base struct{}

// Provide does nothing.
func (Base) Provide(*Ports) {}

// Migrate does nothing.
func (Base) Migrate(context.Context) error { return nil }

// API serves nothing; every operation of the module's tags answers 501.
func (Base) API() API { return API{} }

// Start does nothing.
func (Base) Start(context.Context) error { return nil }

// Stop does nothing.
func (Base) Stop(context.Context) error { return nil }
