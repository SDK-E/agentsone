// SPDX-License-Identifier: MIT

package domain

import (
	"context"
	"encoding/json"
	"time"

	"github.com/SDK-E/agentsone/internal/core"
)

// EventRepository stores the event log. Sequences are assigned on append and
// only ever grow.
type EventRepository interface {
	// Append stores e with the next sequence and returns it as stored.
	// An event id that is already stored is a core.ErrConflict.
	Append(ctx context.Context, e core.Event) (core.Event, error)
	// After returns up to limit events whose sequence is larger than after, oldest first.
	After(ctx context.Context, after uint64, limit int) ([]core.Event, error)
	// Last returns the largest stored sequence, or 0 when the log is empty.
	Last(ctx context.Context) (uint64, error)
}

// EmitterRepository stores emitters.
type EmitterRepository interface {
	// Create stores e. A name that is taken is ErrNameTaken.
	Create(ctx context.Context, e Emitter) error
	// Get returns the emitter or core.ErrNotFound.
	Get(ctx context.Context, id string) (Emitter, error)
	// ByName returns the emitter or core.ErrNotFound.
	ByName(ctx context.Context, name string) (Emitter, error)
	// List returns up to limit emitters with an id after afterID, in id order.
	List(ctx context.Context, afterID string, limit int) ([]Emitter, error)
	// SetEnabled turns an emitter on or off, or returns core.ErrNotFound.
	SetEnabled(ctx context.Context, id string, enabled bool) error
	// Touch records the time of the newest event of the emitter called name.
	Touch(ctx context.Context, name string, at time.Time) error
}

// TriggerRepository stores triggers.
type TriggerRepository interface {
	Create(ctx context.Context, t Trigger) error
	// Get returns the trigger or core.ErrNotFound.
	Get(ctx context.Context, id string) (Trigger, error)
	// List returns up to limit triggers with an id after afterID, in id order.
	List(ctx context.Context, afterID string, limit int) ([]Trigger, error)
	// Enabled returns every enabled trigger.
	Enabled(ctx context.Context) ([]Trigger, error)
	// Update replaces name, filter, action, rate and enabled. Counters stay.
	Update(ctx context.Context, t Trigger) error
	// Delete removes the trigger or returns core.ErrNotFound.
	Delete(ctx context.Context, id string) error
	// RecordFire counts one firing at the given time.
	RecordFire(ctx context.Context, id string, at time.Time) error
	// RecordDrop counts one match refused by the wake-rate limit.
	RecordDrop(ctx context.Context, id string) error
}

// Catalog describes the built-in event types.
type Catalog interface {
	// ScopeKind returns "machine", "team" or "agent" for a built-in type, and false for others.
	ScopeKind(eventType string) (string, bool)
	// ValidateData checks data against the schema of a built-in type. Other types pass.
	ValidateData(eventType string, data json.RawMessage) error
}
