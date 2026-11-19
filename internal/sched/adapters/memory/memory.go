// SPDX-License-Identifier: MIT

// Package memory holds in-memory repositories for tests. They pass the same
// contract suite as the SQLite ones.
package memory

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/SDK-E/agentsone/internal/sched/domain"
)

// Tasks is an in-memory domain.TaskRepository.
type Tasks struct {
	mu    sync.Mutex
	items map[string]domain.Task
}

var _ domain.TaskRepository = (*Tasks)(nil)

// NewTasks returns an empty repository.
func NewTasks() *Tasks { return &Tasks{items: map[string]domain.Task{}} }

// Insert stores a new task.
func (r *Tasks) Insert(_ context.Context, t domain.Task) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.items[t.ID]; ok {
		return fmt.Errorf("task %s: %w", t.ID, domain.ErrConflict)
	}
	r.items[t.ID] = t
	return nil
}

// Update replaces a stored task.
func (r *Tasks) Update(_ context.Context, t domain.Task) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.items[t.ID]; !ok {
		return fmt.Errorf("task %s: %w", t.ID, domain.ErrNotFound)
	}
	r.items[t.ID] = t
	return nil
}

// Get returns a task.
func (r *Tasks) Get(_ context.Context, id string) (domain.Task, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.items[id]
	if !ok {
		return domain.Task{}, fmt.Errorf("task %s: %w", id, domain.ErrNotFound)
	}
	return t, nil
}

// List returns matching tasks, newest first.
func (r *Tasks) List(_ context.Context, f domain.TaskFilter) ([]domain.Task, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := []domain.Task{}
	for _, t := range r.items {
		if matchTask(f, t) {
			out = append(out, t)
		}
	}
	slices.SortFunc(out, func(a, b domain.Task) int { return cmp.Compare(b.ID, a.ID) })
	return limit(out, f.Limit), nil
}

func matchTask(f domain.TaskFilter, t domain.Task) bool {
	return (f.Status == "" || f.Status == t.Status) &&
		(f.Assignee == "" || f.Assignee == t.Assignee) &&
		(f.Source == "" || f.Source == t.Source) &&
		(f.BeforeID == "" || t.ID < f.BeforeID)
}

func limit[T any](items []T, n int) []T {
	if n > 0 && len(items) > n {
		return items[:n]
	}
	return items
}

// Schedules is an in-memory domain.ScheduleRepository.
type Schedules struct {
	mu    sync.Mutex
	items map[string]domain.Schedule
}

var _ domain.ScheduleRepository = (*Schedules)(nil)

// NewSchedules returns an empty repository.
func NewSchedules() *Schedules { return &Schedules{items: map[string]domain.Schedule{}} }

// Insert stores a new schedule.
func (r *Schedules) Insert(_ context.Context, s domain.Schedule) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.items[s.ID]; ok {
		return fmt.Errorf("schedule %s: %w", s.ID, domain.ErrConflict)
	}
	r.items[s.ID] = s
	return nil
}

// Update replaces a stored schedule.
func (r *Schedules) Update(_ context.Context, s domain.Schedule) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.items[s.ID]; !ok {
		return fmt.Errorf("schedule %s: %w", s.ID, domain.ErrNotFound)
	}
	r.items[s.ID] = s
	return nil
}

// Get returns a schedule.
func (r *Schedules) Get(_ context.Context, id string) (domain.Schedule, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.items[id]
	if !ok {
		return domain.Schedule{}, fmt.Errorf("schedule %s: %w", id, domain.ErrNotFound)
	}
	return s, nil
}

// Delete removes a schedule.
func (r *Schedules) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.items[id]; !ok {
		return fmt.Errorf("schedule %s: %w", id, domain.ErrNotFound)
	}
	delete(r.items, id)
	return nil
}

// List returns matching schedules, oldest first.
func (r *Schedules) List(_ context.Context, f domain.ScheduleFilter) ([]domain.Schedule, error) {
	return r.collect(func(s domain.Schedule) bool {
		return (f.Agent == "" || f.Agent == s.Agent) && (f.AfterID == "" || s.ID > f.AfterID)
	}, f.Limit), nil
}

// Due returns enabled schedules whose next run is at or before now.
func (r *Schedules) Due(_ context.Context, now time.Time) ([]domain.Schedule, error) {
	return r.collect(func(s domain.Schedule) bool { return s.Due(now) }, 0), nil
}

func (r *Schedules) collect(keep func(domain.Schedule) bool, n int) []domain.Schedule {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := []domain.Schedule{}
	for _, s := range r.items {
		if keep(s) {
			out = append(out, s)
		}
	}
	slices.SortFunc(out, func(a, b domain.Schedule) int { return cmp.Compare(a.ID, b.ID) })
	return limit(out, n)
}
