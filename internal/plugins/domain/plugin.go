// SPDX-License-Identifier: MIT

package domain

import (
	"context"
	"time"
)

// Health of an installed plugin.
type Health string

// Plugin health values, as in the API.
const (
	HealthOK       Health = "ok"
	HealthDegraded Health = "degraded"
	HealthFailed   Health = "failed"
)

// Plugin is one installed plugin version.
type Plugin struct {
	ID          string // plg_<ULID>
	Name        string
	Version     string
	Kinds       []string
	Source      string // SourceID
	Ref         string
	Enabled     bool
	Health      Health
	InstalledBy Actor
	InstalledAt time.Time
	Manifest    Manifest
	Dir         string // where it lives on disk
	SHA256      string // tree digest at install time
}

// Actor is who did something, as stored.
type Actor struct {
	Kind string // human, agent, system, host, plugin
	Name string
}

// PluginFilter narrows ListPlugins.
type PluginFilter struct {
	Kind    string // empty: any
	Enabled *bool  // nil: any
}

// PluginRepository stores installed plugins. Implementations must be safe for
// concurrent use. Paging is by ID, ascending: After is the last ID of the
// previous page.
type PluginRepository interface {
	// Insert stores a new plugin. ErrAlreadyInstalled when the name and
	// version, or the ID, exist.
	Insert(ctx context.Context, p Plugin) error
	// Get returns ErrNotFound when the ID is unknown.
	Get(ctx context.Context, id string) (Plugin, error)
	// ByName returns every installed version of a name, newest install first.
	ByName(ctx context.Context, name string) ([]Plugin, error)
	// List returns up to limit plugins with ID > after that match the filter.
	List(ctx context.Context, f PluginFilter, after string, limit int) ([]Plugin, error)
	// SetEnabled flips the enabled flag and health. ErrNotFound when unknown.
	SetEnabled(ctx context.Context, id string, enabled bool, health Health) error
	// Delete removes the record. ErrNotFound when unknown.
	Delete(ctx context.Context, id string) error
}
