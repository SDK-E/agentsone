// SPDX-License-Identifier: MIT

package domain

import (
	"context"
	"errors"
	"time"
)

// ErrConflict is an insert of an id that already exists.
var ErrConflict = errors.New("already exists")

// TaskFilter selects tasks. Empty fields match everything.
type TaskFilter struct {
	Status   Status
	Assignee string
	Source   Source
	// BeforeID returns only tasks with a smaller id, for paging.
	BeforeID string
	// Limit is the most tasks to return; 0 means no limit.
	Limit int
}

// TaskRepository stores tasks. Every implementation passes
// schedtest.RunTaskRepositoryContract.
type TaskRepository interface {
	// Insert stores a new task; an existing id is ErrConflict.
	Insert(ctx context.Context, t Task) error
	// Update replaces a stored task; an unknown id is ErrNotFound.
	Update(ctx context.Context, t Task) error
	// Get returns a task, or ErrNotFound.
	Get(ctx context.Context, id string) (Task, error)
	// List returns matching tasks, newest first (largest id first).
	List(ctx context.Context, f TaskFilter) ([]Task, error)
}

// ScheduleFilter selects schedules. Empty fields match everything.
type ScheduleFilter struct {
	Agent string
	// AfterID returns only schedules with a larger id, for paging.
	AfterID string
	// Limit is the most schedules to return; 0 means no limit.
	Limit int
}

// ScheduleRepository stores schedules. Every implementation passes
// schedtest.RunScheduleRepositoryContract.
type ScheduleRepository interface {
	// Insert stores a new schedule; an existing id is ErrConflict.
	Insert(ctx context.Context, s Schedule) error
	// Update replaces a stored schedule; an unknown id is ErrNotFound.
	Update(ctx context.Context, s Schedule) error
	// Get returns a schedule, or ErrNotFound.
	Get(ctx context.Context, id string) (Schedule, error)
	// Delete removes a schedule, or returns ErrNotFound.
	Delete(ctx context.Context, id string) error
	// List returns matching schedules, oldest first (smallest id first).
	List(ctx context.Context, f ScheduleFilter) ([]Schedule, error)
	// Due returns enabled schedules whose next run is at or before now.
	Due(ctx context.Context, now time.Time) ([]Schedule, error)
}
