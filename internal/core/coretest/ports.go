// SPDX-License-Identifier: MIT

package coretest

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/SDK-E/agentsone/internal/core"
)

// Tasks is an in-memory core.TaskService.
type Tasks struct {
	mu      sync.Mutex
	clock   core.Clock
	ids     core.IDs
	created []core.Task
}

var _ core.TaskService = (*Tasks)(nil)

// NewTasks returns a TaskService that stamps tasks with clock and ids.
func NewTasks(clock core.Clock, ids core.IDs) *Tasks { return &Tasks{clock: clock, ids: ids} }

// CreateTask stores an open task.
func (s *Tasks) CreateTask(_ context.Context, t core.NewTask) (core.Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	task := core.Task{
		ID:       s.ids.New("tsk"),
		Title:    t.Title,
		Assignee: t.Assignee,
		Status:   "open",
		Created:  s.clock.Now(),
	}
	s.created = append(s.created, task)
	return task, nil
}

// Created returns created tasks in order.
func (s *Tasks) Created() []core.Task {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]core.Task(nil), s.created...)
}

// Audit is an in-memory core.AuditLog without hashing.
type Audit struct {
	mu      sync.Mutex
	entries []core.AuditEntry
}

var _ core.AuditLog = (*Audit)(nil)

// NewAudit returns an empty log.
func NewAudit() *Audit { return &Audit{} }

// Append records e.
func (a *Audit) Append(_ context.Context, e core.AuditEntry) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.entries = append(a.entries, e)
	return nil
}

// Entries returns recorded entries in order.
func (a *Audit) Entries() []core.AuditEntry {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]core.AuditEntry(nil), a.entries...)
}

// Live is an in-memory core.LiveList.
type Live struct {
	mu    sync.Mutex
	clock core.Clock
	lines map[string]core.LiveLine
}

var _ core.LiveList = (*Live)(nil)

// NewLive returns an empty live list stamped by clock.
func NewLive(clock core.Clock) *Live { return &Live{clock: clock, lines: map[string]core.LiveLine{}} }

// UpdateLine sets the agent's line, rejecting lines over core.MaxLiveLine.
func (l *Live) UpdateLine(_ context.Context, agent, line string) error {
	if len(line) > core.MaxLiveLine {
		return fmt.Errorf("line of %d bytes, limit %d: %w", len(line), core.MaxLiveLine, core.ErrInvalid)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines[agent] = core.LiveLine{Agent: agent, Line: line, Updated: l.clock.Now()}
	return nil
}

// Lines returns every line sorted by agent.
func (l *Live) Lines(context.Context) ([]core.LiveLine, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	lines := make([]core.LiveLine, 0, len(l.lines))
	for _, line := range l.lines {
		lines = append(lines, line)
	}
	sort.Slice(lines, func(i, j int) bool { return lines[i].Agent < lines[j].Agent })
	return lines, nil
}

// Budget is an in-memory core.BudgetView.
type Budget struct {
	mu     sync.Mutex
	byName map[string]core.Budget
}

var _ core.BudgetView = (*Budget)(nil)

// NewBudget returns a view with no allocations.
func NewBudget() *Budget { return &Budget{byName: map[string]core.Budget{}} }

// Set stores b for b.Agent.
func (v *Budget) Set(b core.Budget) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.byName[b.Agent] = b
}

// Allocation returns the agent's budget.
func (v *Budget) Allocation(_ context.Context, agent string) (core.Budget, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	b, ok := v.byName[agent]
	if !ok {
		return core.Budget{}, fmt.Errorf("budget for %q: %w", agent, core.ErrNotFound)
	}
	return b, nil
}

// Runners is an in-memory core.RunnerRegistry.
type Runners struct {
	mu     sync.Mutex
	byName map[string]core.RunnerAdapter
}

var _ core.RunnerRegistry = (*Runners)(nil)

// NewRunners returns an empty registry.
func NewRunners() *Runners { return &Runners{byName: map[string]core.RunnerAdapter{}} }

// Register adds a, keyed by a.Info().Name.
func (r *Runners) Register(a core.RunnerAdapter) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.byName[a.Info().Name] = a
}

// Runner returns the named adapter.
func (r *Runners) Runner(_ context.Context, name string) (core.RunnerAdapter, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	a, ok := r.byName[name]
	if !ok {
		return nil, fmt.Errorf("runner %q: %w", name, core.ErrNotFound)
	}
	return a, nil
}

// Runners lists adapters sorted by name.
func (r *Runners) Runners(context.Context) ([]core.RunnerInfo, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	infos := make([]core.RunnerInfo, 0, len(r.byName))
	for _, a := range r.byName {
		infos = append(infos, a.Info())
	}
	sort.Slice(infos, func(i, j int) bool { return infos[i].Name < infos[j].Name })
	return infos, nil
}
