// SPDX-License-Identifier: MIT

// Package memory holds in-memory router repositories for tests.
package memory

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/router/domain"
)

// Lines is an in-memory domain.LineRepository.
type Lines struct {
	mu    sync.Mutex
	lines map[string]domain.StoredLine
}

var _ domain.LineRepository = (*Lines)(nil)

// NewLines returns an empty repository.
func NewLines() *Lines { return &Lines{lines: map[string]domain.StoredLine{}} }

// Put sets the agent's line.
func (r *Lines) Put(_ context.Context, l domain.StoredLine) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lines[l.Agent] = l
	return nil
}

// List returns every line sorted by agent.
func (r *Lines) List(context.Context) ([]domain.StoredLine, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]domain.StoredLine, 0, len(r.lines))
	for _, l := range r.lines {
		out = append(out, l)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Agent < out[j].Agent })
	return out, nil
}

// Delete forgets the agent's line.
func (r *Lines) Delete(_ context.Context, agent string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.lines, agent)
	return nil
}

// Assignments is an in-memory domain.AssignmentRepository.
type Assignments struct {
	mu   sync.Mutex
	byID map[string]domain.Assignment
}

var _ domain.AssignmentRepository = (*Assignments)(nil)

// NewAssignments returns an empty repository.
func NewAssignments() *Assignments { return &Assignments{byID: map[string]domain.Assignment{}} }

// Save stores a.
func (r *Assignments) Save(_ context.Context, a domain.Assignment) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.byID[a.ID] = a
	return nil
}

// Get returns the assignment with id.
func (r *Assignments) Get(_ context.Context, id string) (domain.Assignment, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	a, ok := r.byID[id]
	if !ok {
		return domain.Assignment{}, fmt.Errorf("assignment %s: %w", id, core.ErrNotFound)
	}
	return a, nil
}
