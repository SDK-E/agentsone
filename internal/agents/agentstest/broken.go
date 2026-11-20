// SPDX-License-Identifier: MIT

package agentstest

import (
	"context"
	"time"

	"github.com/SDK-E/agentsone/internal/agents/domain"
)

// Broken wraps a repository and makes the methods named in Fail return Err,
// so tests can see that a storage failure reaches the caller.
type Broken struct {
	domain.Repository
	Err  error
	Fail map[string]bool
}

func (b Broken) fail(method string) error {
	if b.Fail[method] {
		return b.Err
	}
	return nil
}

// ListAgents fails when "ListAgents" is in Fail.
func (b Broken) ListAgents(ctx context.Context) ([]domain.Agent, error) {
	if err := b.fail("ListAgents"); err != nil {
		return nil, err
	}
	return b.Repository.ListAgents(ctx)
}

// HireAgent fails when "HireAgent" is in Fail.
func (b Broken) HireAgent(ctx context.Context, a domain.Agent, ev domain.Outbound) error {
	if err := b.fail("HireAgent"); err != nil {
		return err
	}
	return b.Repository.HireAgent(ctx, a, ev)
}

// PendingEvents fails when "PendingEvents" is in Fail.
func (b Broken) PendingEvents(ctx context.Context, limit int) ([]domain.Outbound, error) {
	if err := b.fail("PendingEvents"); err != nil {
		return nil, err
	}
	return b.Repository.PendingEvents(ctx, limit)
}

// MarkSent fails when "MarkSent" is in Fail.
func (b Broken) MarkSent(ctx context.Context, id string, at time.Time) error {
	if err := b.fail("MarkSent"); err != nil {
		return err
	}
	return b.Repository.MarkSent(ctx, id, at)
}

// SaveAgents fails when "SaveAgents" is in Fail.
func (b Broken) SaveAgents(ctx context.Context, agents ...domain.Agent) error {
	if err := b.fail("SaveAgents"); err != nil {
		return err
	}
	return b.Repository.SaveAgents(ctx, agents...)
}

// Charter fails when "Charter" is in Fail.
func (b Broken) Charter(ctx context.Context) (domain.Charter, error) {
	if err := b.fail("Charter"); err != nil {
		return domain.Charter{}, err
	}
	return b.Repository.Charter(ctx)
}

// SaveCharter fails when "SaveCharter" is in Fail.
func (b Broken) SaveCharter(ctx context.Context, c domain.Charter) error {
	if err := b.fail("SaveCharter"); err != nil {
		return err
	}
	return b.Repository.SaveCharter(ctx, c)
}

// SaveGoal fails when "SaveGoal" is in Fail.
func (b Broken) SaveGoal(ctx context.Context, g domain.Goal) error {
	if err := b.fail("SaveGoal"); err != nil {
		return err
	}
	return b.Repository.SaveGoal(ctx, g)
}

// Pool fails when "Pool" is in Fail.
func (b Broken) Pool(ctx context.Context) (domain.Pool, error) {
	if err := b.fail("Pool"); err != nil {
		return domain.Pool{}, err
	}
	return b.Repository.Pool(ctx)
}

// Allocations fails when "Allocations" is in Fail.
func (b Broken) Allocations(ctx context.Context) (map[string]int64, error) {
	if err := b.fail("Allocations"); err != nil {
		return nil, err
	}
	return b.Repository.Allocations(ctx)
}

// SetAllocation fails when "SetAllocation" is in Fail.
func (b Broken) SetAllocation(ctx context.Context, agent string, amount *int64) error {
	if err := b.fail("SetAllocation"); err != nil {
		return err
	}
	return b.Repository.SetAllocation(ctx, agent, amount)
}

// Usage fails when "Usage" is in Fail.
func (b Broken) Usage(ctx context.Context) (map[string]int64, error) {
	if err := b.fail("Usage"); err != nil {
		return nil, err
	}
	return b.Repository.Usage(ctx)
}

// AppendChat fails when "AppendChat" is in Fail.
func (b Broken) AppendChat(ctx context.Context, m domain.ChatMessage) error {
	if err := b.fail("AppendChat"); err != nil {
		return err
	}
	return b.Repository.AppendChat(ctx, m)
}

// Freezes fails when "Freezes" is in Fail.
func (b Broken) Freezes(ctx context.Context) ([]string, error) {
	if err := b.fail("Freezes"); err != nil {
		return nil, err
	}
	return b.Repository.Freezes(ctx)
}

// AddFreeze fails when "AddFreeze" is in Fail.
func (b Broken) AddFreeze(ctx context.Context, scope string) error {
	if err := b.fail("AddFreeze"); err != nil {
		return err
	}
	return b.Repository.AddFreeze(ctx, scope)
}
