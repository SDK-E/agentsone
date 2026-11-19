// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/SDK-E/agentsone/internal/agents/domain"
	"github.com/SDK-E/agentsone/internal/core"
)

// Budget is the soft budget split. The host pushes the total (the hard
// wall, held by the gateway on the host); agents split it down the chart.
type Budget struct{ s *Society }

var _ core.BudgetView = Budget{}

// Budget returns the budget use cases. It is also the BudgetView port.
func (s *Society) Budget() Budget { return Budget{s: s} }

func (s *Society) split(ctx context.Context) (domain.Split, error) {
	chart, err := s.chart(ctx)
	if err != nil {
		return domain.Split{}, err
	}
	pool, err := s.repo.Pool(ctx)
	if err != nil {
		return domain.Split{}, fmt.Errorf("read budget pool: %w", err)
	}
	alloc, err := s.repo.Allocations(ctx)
	if err != nil {
		return domain.Split{}, fmt.Errorf("read allocations: %w", err)
	}
	usage, err := s.repo.Usage(ctx)
	if err != nil {
		return domain.Split{}, fmt.Errorf("read usage: %w", err)
	}
	return domain.NewSplit(chart, pool, alloc, usage), nil
}

// PushPool stores the total and defence state the host gateway pushed.
// Only the host (through the door) or the daemon may push it.
func (b Budget) PushPool(ctx context.Context, p domain.Pool) error {
	c, _ := core.CallerFrom(ctx)
	if c.Kind != core.CallerHost && c.Kind != core.CallerSystem {
		return fmt.Errorf("the budget total is pushed by the host: %w", core.ErrForbidden)
	}
	if err := p.Validate(); err != nil {
		return err
	}
	p.Known = true
	if err := b.s.repo.SavePool(ctx, p); err != nil {
		return fmt.Errorf("save budget pool: %w", err)
	}
	return nil
}

// Pool returns the last pushed total, or core.ErrUnavailable before the
// host pushes one.
func (b Budget) Pool(ctx context.Context) (domain.Pool, error) {
	p, err := b.s.repo.Pool(ctx)
	if err != nil {
		return domain.Pool{}, fmt.Errorf("read budget pool: %w", err)
	}
	if !p.Known {
		return domain.Pool{}, fmt.Errorf("the host has not pushed a budget yet: %w", core.ErrUnavailable)
	}
	return p, nil
}

// Split returns every agent's share.
func (b Budget) Split(ctx context.Context) (domain.Split, error) { return b.s.split(ctx) }

// Allocate sets the soft allocation of an agent below the calling agent.
// Nil shares the manager's remainder.
func (b Budget) Allocate(ctx context.Context, target string, amount *int64) (domain.BudgetNode, error) {
	name, err := actor(ctx)
	if err != nil {
		return domain.BudgetNode{}, err
	}
	b.s.mu.Lock()
	defer b.s.mu.Unlock()
	split, err := b.s.split(ctx)
	if err != nil {
		return domain.BudgetNode{}, err
	}
	next, err := split.Allocate(name, target, amount)
	if err != nil {
		return domain.BudgetNode{}, err
	}
	if err := b.s.repo.SetAllocation(ctx, target, amount); err != nil {
		return domain.BudgetNode{}, fmt.Errorf("save allocation of %s: %w", target, err)
	}
	raw, err := json.Marshal(map[string]any{"allocated_micros": amount, "wall": "soft"})
	if err != nil {
		return domain.BudgetNode{}, fmt.Errorf("encode allocation: %w", err)
	}
	if err := b.s.ports.Audit.Append(ctx, core.AuditEntry{Actor: "agent:" + name, Action: "budget.allocate", Target: "agent:" + target, Detail: raw}); err != nil {
		return domain.BudgetNode{}, fmt.Errorf("audit budget.allocate: %w", err)
	}
	return next.Node(target), nil
}

// RecordUsage adds spending reported by an agent's runner (usage
// notifications through the supervisor).
func (b Budget) RecordUsage(ctx context.Context, agent string, micros int64) error {
	if micros < 0 {
		return fmt.Errorf("%w: usage %d below zero", domain.ErrInvalidBudget, micros)
	}
	if _, err := b.s.repo.GetAgent(ctx, agent); err != nil {
		return err
	}
	if err := b.s.repo.AddUsage(ctx, agent, micros); err != nil {
		return fmt.Errorf("record usage of %s: %w", agent, err)
	}
	return nil
}

// Allocation returns agent's share for the BudgetView port.
func (b Budget) Allocation(ctx context.Context, agent string) (core.Budget, error) {
	split, err := b.s.split(ctx)
	if err != nil {
		return core.Budget{}, err
	}
	if _, err := b.s.repo.GetAgent(ctx, agent); err != nil {
		return core.Budget{}, err
	}
	return split.Core(agent), nil
}
