// SPDX-License-Identifier: MIT

// Package memory is an in-memory domain.Repository for tests and for a
// daemon run without a database. It passes agentstest.RunRepositoryContract.
package memory

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/SDK-E/agentsone/internal/agents/domain"
)

// Repository holds the society in memory.
type Repository struct {
	mu      sync.Mutex
	agents  map[string]domain.Agent
	charter domain.Charter
	goals   map[string]domain.Goal
	pool    domain.Pool
	alloc   map[string]int64
	usage   map[string]int64
	chat    []domain.ChatMessage
	freezes map[string]struct{}
	seeded  bool
	// outbox holds stored events in write order; sent marks the published.
	outbox []domain.Outbound
	sent   map[string]bool
	fail   error
}

// HireAgent stores a and ev together, or neither.
func (r *Repository) HireAgent(_ context.Context, a domain.Agent, ev domain.Outbound) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.takeFail(); err != nil {
		return err
	}
	if _, ok := r.agents[a.Name]; ok {
		return fmt.Errorf("%w: %s", domain.ErrNameTaken, a.Name)
	}
	if slices.ContainsFunc(r.outbox, func(o domain.Outbound) bool { return o.ID == ev.ID }) {
		return fmt.Errorf("outbox event %s exists", ev.ID)
	}
	r.agents[a.Name] = copyAgent(a)
	ev.Data = slices.Clone(ev.Data)
	r.outbox = append(r.outbox, ev)
	return nil
}

// PendingEvents returns up to limit unsent events, oldest first.
func (r *Repository) PendingEvents(_ context.Context, limit int) ([]domain.Outbound, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := []domain.Outbound{}
	for _, ev := range r.outbox {
		if !r.sent[ev.ID] {
			ev.Data = slices.Clone(ev.Data)
			out = append(out, ev)
		}
	}
	slices.SortStableFunc(out, func(a, b domain.Outbound) int { return a.CreatedAt.Compare(b.CreatedAt) })
	return out[:min(limit, len(out))], nil
}

// MarkSent marks the event published.
func (r *Repository) MarkSent(_ context.Context, id string, _ time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !slices.ContainsFunc(r.outbox, func(o domain.Outbound) bool { return o.ID == id }) {
		return fmt.Errorf("%w: %s", domain.ErrEventNotFound, id)
	}
	r.sent[id] = true
	return nil
}

// SeedApplied reports whether the seed was recorded.
func (r *Repository) SeedApplied(context.Context) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.seeded, nil
}

// MarkSeedApplied records the seed; it keeps the first record.
func (r *Repository) MarkSeedApplied(context.Context, time.Time) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	first := !r.seeded
	r.seeded = true
	return first, nil
}

// Freezes returns the active freeze scopes, sorted.
func (r *Repository) Freezes(context.Context) ([]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Sorted(maps.Keys(r.freezes)), nil
}

// AddFreeze stores scope; it is a no-op for an active scope.
func (r *Repository) AddFreeze(_ context.Context, scope string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.freezes[scope] = struct{}{}
	return nil
}

// RemoveFreeze removes scope; it is a no-op for an inactive scope.
func (r *Repository) RemoveFreeze(_ context.Context, scope string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.freezes, scope)
	return nil
}

// FailNext makes the next agent write (CreateAgent, HireAgent or SaveAgents) return
// err and store nothing, so tests can see how callers handle a failed store.
func (r *Repository) FailNext(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.fail = err
}

// takeFail returns and clears the injected failure. Call it with mu held.
func (r *Repository) takeFail() error {
	err := r.fail
	r.fail = nil
	return err
}

var _ domain.Repository = (*Repository)(nil)

// New returns an empty repository.
func New() *Repository {
	return &Repository{
		agents: map[string]domain.Agent{}, goals: map[string]domain.Goal{},
		alloc: map[string]int64{}, usage: map[string]int64{}, freezes: map[string]struct{}{},
		sent: map[string]bool{},
	}
}

// copyAgent returns a with its pointer fields copied, so callers cannot
// change stored values through an alias.
func copyAgent(a domain.Agent) domain.Agent {
	if a.Limits != nil {
		l := *a.Limits
		a.Limits = &l
	}
	if a.LastTurnAt != nil {
		t := *a.LastTurnAt
		a.LastTurnAt = &t
	}
	return a
}

// ListAgents returns every agent sorted by name.
func (r *Repository) ListAgents(context.Context) ([]domain.Agent, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]domain.Agent, 0, len(r.agents))
	for _, a := range r.agents {
		out = append(out, copyAgent(a))
	}
	slices.SortFunc(out, func(a, b domain.Agent) int { return strings.Compare(a.Name, b.Name) })
	return out, nil
}

// GetAgent returns the named agent.
func (r *Repository) GetAgent(_ context.Context, name string) (domain.Agent, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	a, ok := r.agents[name]
	if !ok {
		return domain.Agent{}, fmt.Errorf("%w: %s", domain.ErrAgentNotFound, name)
	}
	return copyAgent(a), nil
}

// CreateAgent stores a new agent.
func (r *Repository) CreateAgent(_ context.Context, a domain.Agent) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.takeFail(); err != nil {
		return err
	}
	if _, ok := r.agents[a.Name]; ok {
		return fmt.Errorf("%w: %s", domain.ErrNameTaken, a.Name)
	}
	r.agents[a.Name] = copyAgent(a)
	return nil
}

// SaveAgents replaces existing agents, all or none.
func (r *Repository) SaveAgents(_ context.Context, agents ...domain.Agent) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.takeFail(); err != nil {
		return err
	}
	for _, a := range agents {
		if _, ok := r.agents[a.Name]; !ok {
			return fmt.Errorf("%w: %s", domain.ErrAgentNotFound, a.Name)
		}
	}
	for _, a := range agents {
		r.agents[a.Name] = copyAgent(a)
	}
	return nil
}

// Charter returns the stored charter.
func (r *Repository) Charter(context.Context) (domain.Charter, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.charter, nil
}

// SaveCharter stores c when it is the next version.
func (r *Repository) SaveCharter(_ context.Context, c domain.Charter) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if c.Version != r.charter.Version+1 {
		return fmt.Errorf("%w: version %d after %d", domain.ErrStaleVersion, c.Version, r.charter.Version)
	}
	r.charter = c
	return nil
}

// CreateGoal stores g.
func (r *Repository) CreateGoal(_ context.Context, g domain.Goal) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.goals[g.ID]; ok {
		return errors.New("goal " + g.ID + " exists")
	}
	r.goals[g.ID] = g
	return nil
}

// GetGoal returns the goal with id.
func (r *Repository) GetGoal(_ context.Context, id string) (domain.Goal, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	g, ok := r.goals[id]
	if !ok {
		return domain.Goal{}, fmt.Errorf("%w: %s", domain.ErrGoalNotFound, id)
	}
	return g, nil
}

// SaveGoal replaces an existing goal.
func (r *Repository) SaveGoal(_ context.Context, g domain.Goal) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.goals[g.ID]; !ok {
		return fmt.Errorf("%w: %s", domain.ErrGoalNotFound, g.ID)
	}
	r.goals[g.ID] = g
	return nil
}

// DeleteGoal removes the goal with id.
func (r *Repository) DeleteGoal(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.goals[id]; !ok {
		return fmt.Errorf("%w: %s", domain.ErrGoalNotFound, id)
	}
	delete(r.goals, id)
	return nil
}

// ListGoals returns matching goals sorted by ID.
func (r *Repository) ListGoals(_ context.Context, f domain.GoalFilter) ([]domain.Goal, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := []domain.Goal{}
	for _, id := range slices.Sorted(maps.Keys(r.goals)) {
		g := r.goals[id]
		if (f.Owner != "" && g.Owner != f.Owner) || (f.Status != "" && g.Status != f.Status) || (f.AfterID != "" && id <= f.AfterID) {
			continue
		}
		if f.Limit > 0 && len(out) == f.Limit {
			break
		}
		out = append(out, g)
	}
	return out, nil
}

// Pool returns the last pushed pool.
func (r *Repository) Pool(context.Context) (domain.Pool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.pool, nil
}

// SavePool stores p.
func (r *Repository) SavePool(_ context.Context, p domain.Pool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pool = p
	return nil
}

// Allocations returns a copy of every explicit allocation.
func (r *Repository) Allocations(context.Context) (map[string]int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return maps.Clone(r.alloc), nil
}

// SetAllocation stores or, for nil, removes agent's allocation.
func (r *Repository) SetAllocation(_ context.Context, agent string, amount *int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if amount == nil {
		delete(r.alloc, agent)
		return nil
	}
	r.alloc[agent] = *amount
	return nil
}

// Usage returns a copy of recorded spending per agent.
func (r *Repository) Usage(context.Context) (map[string]int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return maps.Clone(r.usage), nil
}

// AddUsage adds micros to agent's spending.
func (r *Repository) AddUsage(_ context.Context, agent string, micros int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.usage[agent] += micros
	return nil
}

// AppendChat stores m.
func (r *Repository) AppendChat(_ context.Context, m domain.ChatMessage) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.chat = append(r.chat, m)
	return nil
}

// ListChat returns agent's messages newest first.
func (r *Repository) ListChat(_ context.Context, agent, beforeID string, limit int) ([]domain.ChatMessage, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	mine := []domain.ChatMessage{}
	for _, m := range r.chat {
		if m.Agent == agent && (beforeID == "" || m.ID < beforeID) {
			mine = append(mine, m)
		}
	}
	slices.SortFunc(mine, func(a, b domain.ChatMessage) int { return strings.Compare(b.ID, a.ID) })
	if limit > 0 && len(mine) > limit {
		mine = mine[:limit]
	}
	return mine, nil
}
