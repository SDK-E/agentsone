// SPDX-License-Identifier: MIT

package app

import (
	"context"

	"github.com/SDK-E/agentsone/internal/agents/domain"
	"github.com/SDK-E/agentsone/internal/core"
)

// Directory is the society's core.AgentDirectory port.
type Directory struct{ s *Society }

var _ core.AgentDirectory = Directory{}

// Directory returns the AgentDirectory port over the society.
func (s *Society) Directory() Directory { return Directory{s: s} }

// GetAgent returns the named agent, retired or not.
func (d Directory) GetAgent(ctx context.Context, name string) (core.Agent, error) {
	a, err := d.s.repo.GetAgent(ctx, name)
	if err != nil {
		return core.Agent{}, err
	}
	return a.Core(), nil
}

// ListAgents returns the active agents sorted by name.
func (d Directory) ListAgents(ctx context.Context) ([]core.Agent, error) {
	list, err := d.s.List(ctx, ListFilter{})
	if err != nil {
		return nil, err
	}
	out := make([]core.Agent, 0, len(list))
	for _, a := range list {
		out = append(out, a.Core())
	}
	return out, nil
}

// Manager returns the agent's manager, or "" for the founder.
func (d Directory) Manager(ctx context.Context, name string) (string, error) {
	a, err := d.s.repo.GetAgent(ctx, name)
	return a.Manager, err
}

// IsBelow reports whether name is strictly below ancestor.
func (d Directory) IsBelow(ctx context.Context, name, ancestor string) (bool, error) {
	chart, err := d.chartWith(ctx, name)
	if err != nil {
		return false, err
	}
	return chart.IsBelow(name, ancestor), nil
}

// TeamRoot returns the head of name's chart branch: the agent directly
// below the founder that holds name (the contract's `team` field).
func (d Directory) TeamRoot(ctx context.Context, name string) (string, error) {
	chart, err := d.chartWith(ctx, name)
	if err != nil {
		return "", err
	}
	return chart.Team(name), nil
}

func (d Directory) chartWith(ctx context.Context, name string) (domain.Chart, error) {
	chart, err := d.s.chart(ctx)
	if err != nil {
		return domain.Chart{}, err
	}
	if _, err := chart.Get(name); err != nil {
		return domain.Chart{}, err
	}
	return chart, nil
}
