// SPDX-License-Identifier: MIT

// Package memory holds in-memory events repositories for tests and fakes.
// They pass the same contract suites as the SQLite ones.
package memory

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/events/domain"
)

// Events is an in-memory domain.EventRepository.
type Events struct {
	mu     sync.Mutex
	events []core.Event
}

var _ domain.EventRepository = (*Events)(nil)

// NewEvents returns an empty event log.
func NewEvents() *Events { return &Events{} }

// Append stores e with the next sequence.
func (r *Events) Append(_ context.Context, e core.Event) (core.Event, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, stored := range r.events {
		if stored.ID == e.ID {
			return core.Event{}, fmt.Errorf("event %s: %w", e.ID, core.ErrConflict)
		}
	}
	e.Sequence = core.FormatSequence(uint64(len(r.events)) + 1)
	r.events = append(r.events, e)
	return e, nil
}

// After returns up to limit events after sequence after.
func (r *Events) After(_ context.Context, after uint64, limit int) ([]core.Event, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if after >= uint64(len(r.events)) {
		return nil, nil
	}
	rest := r.events[after:]
	if len(rest) > limit {
		rest = rest[:limit]
	}
	return slices.Clone(rest), nil
}

// Last returns the largest stored sequence.
func (r *Events) Last(context.Context) (uint64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return uint64(len(r.events)), nil
}

// Emitters is an in-memory domain.EmitterRepository.
type Emitters struct {
	mu   sync.Mutex
	byID map[string]domain.Emitter
}

var _ domain.EmitterRepository = (*Emitters)(nil)

// NewEmitters returns an empty emitter store.
func NewEmitters() *Emitters { return &Emitters{byID: map[string]domain.Emitter{}} }

// Create stores e.
func (r *Emitters) Create(_ context.Context, e domain.Emitter) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, other := range r.byID {
		if other.Name == e.Name {
			return fmt.Errorf("emitter %q: %w", e.Name, domain.ErrNameTaken)
		}
	}
	r.byID[e.ID] = clone(e)
	return nil
}

// Get returns the emitter with id.
func (r *Emitters) Get(_ context.Context, id string) (domain.Emitter, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.byID[id]
	if !ok {
		return domain.Emitter{}, fmt.Errorf("emitter %s: %w", id, core.ErrNotFound)
	}
	return clone(e), nil
}

// ByName returns the emitter called name.
func (r *Emitters) ByName(_ context.Context, name string) (domain.Emitter, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, e := range r.byID {
		if e.Name == name {
			return clone(e), nil
		}
	}
	return domain.Emitter{}, fmt.Errorf("emitter %q: %w", name, core.ErrNotFound)
}

// List returns up to limit emitters after afterID.
func (r *Emitters) List(_ context.Context, afterID string, limit int) ([]domain.Emitter, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []domain.Emitter
	for id, e := range r.byID {
		if id > afterID {
			out = append(out, clone(e))
		}
	}
	slices.SortFunc(out, func(a, b domain.Emitter) int { return compare(a.ID, b.ID) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// SetEnabled turns an emitter on or off.
func (r *Emitters) SetEnabled(_ context.Context, id string, enabled bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.byID[id]
	if !ok {
		return fmt.Errorf("emitter %s: %w", id, core.ErrNotFound)
	}
	e.Enabled = enabled
	r.byID[id] = e
	return nil
}

// Touch records the time of the newest event of emitter name.
func (r *Emitters) Touch(_ context.Context, name string, at time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, e := range r.byID {
		if e.Name == name {
			e.LastEventAt = &at
			r.byID[id] = e
		}
	}
	return nil
}

func clone(e domain.Emitter) domain.Emitter {
	e.EventTypes = slices.Clone(e.EventTypes)
	e.Command = slices.Clone(e.Command)
	return e
}

func compare(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

// Triggers is an in-memory domain.TriggerRepository.
type Triggers struct {
	mu   sync.Mutex
	byID map[string]domain.Trigger
}

var _ domain.TriggerRepository = (*Triggers)(nil)

// NewTriggers returns an empty trigger store.
func NewTriggers() *Triggers { return &Triggers{byID: map[string]domain.Trigger{}} }

// Create stores t.
func (r *Triggers) Create(_ context.Context, t domain.Trigger) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.byID[t.ID] = t
	return nil
}

// Get returns the trigger with id.
func (r *Triggers) Get(_ context.Context, id string) (domain.Trigger, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.byID[id]
	if !ok {
		return domain.Trigger{}, fmt.Errorf("trigger %s: %w", id, core.ErrNotFound)
	}
	return t, nil
}

// List returns up to limit triggers after afterID.
func (r *Triggers) List(_ context.Context, afterID string, limit int) ([]domain.Trigger, error) {
	return r.collect(func(t domain.Trigger) bool { return t.ID > afterID }, limit), nil
}

// Enabled returns every enabled trigger.
func (r *Triggers) Enabled(context.Context) ([]domain.Trigger, error) {
	return r.collect(func(t domain.Trigger) bool { return t.Enabled }, -1), nil
}

func (r *Triggers) collect(keep func(domain.Trigger) bool, limit int) []domain.Trigger {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []domain.Trigger
	for _, t := range r.byID {
		if keep(t) {
			out = append(out, t)
		}
	}
	slices.SortFunc(out, func(a, b domain.Trigger) int { return compare(a.ID, b.ID) })
	if limit >= 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// Update replaces the editable fields of t.
func (r *Triggers) Update(_ context.Context, t domain.Trigger) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	old, ok := r.byID[t.ID]
	if !ok {
		return fmt.Errorf("trigger %s: %w", t.ID, core.ErrNotFound)
	}
	t.FiredCount, t.DroppedCount, t.LastFiredAt, t.CreatedAt, t.Owner = old.FiredCount, old.DroppedCount, old.LastFiredAt, old.CreatedAt, old.Owner
	r.byID[t.ID] = t
	return nil
}

// Delete removes the trigger.
func (r *Triggers) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.byID[id]; !ok {
		return fmt.Errorf("trigger %s: %w", id, core.ErrNotFound)
	}
	delete(r.byID, id)
	return nil
}

// RecordFire counts a firing.
func (r *Triggers) RecordFire(_ context.Context, id string, at time.Time) error {
	return r.change(id, func(t *domain.Trigger) { t.FiredCount++; t.LastFiredAt = &at })
}

// RecordDrop counts a refused match.
func (r *Triggers) RecordDrop(_ context.Context, id string) error {
	return r.change(id, func(t *domain.Trigger) { t.DroppedCount++ })
}

func (r *Triggers) change(id string, f func(*domain.Trigger)) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.byID[id]
	if !ok {
		return fmt.Errorf("trigger %s: %w", id, core.ErrNotFound)
	}
	f(&t)
	r.byID[id] = t
	return nil
}
