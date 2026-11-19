// SPDX-License-Identifier: MIT

package agents

import (
	"context"
	"errors"
	"fmt"

	"github.com/SDK-E/agentsone/internal/agents/app"
	"github.com/SDK-E/agentsone/internal/agents/domain"
	supapp "github.com/SDK-E/agentsone/internal/agents/supervisor/app"
	"github.com/SDK-E/agentsone/internal/core"
)

// startingOneLine is the welcome's one-line for an agent that has not set
// its own yet (the protocol needs one).
const startingOneLine = "Now: starting up."

// identity is the supervisor's Identity port over the society: who the
// agent is, for the runner's welcome.
type identity struct{ s *app.Society }

// Identity returns the agent's directory entry, team, the charter, its
// one-line and its open goals.
func (i identity) Identity(ctx context.Context, agent string) (supapp.AgentIdentity, error) {
	a, err := i.s.Get(ctx, agent)
	if err != nil {
		return supapp.AgentIdentity{}, err
	}
	chart, err := i.s.Chart(ctx)
	if err != nil {
		return supapp.AgentIdentity{}, err
	}
	charter, err := i.s.Charter(ctx)
	if err != nil {
		return supapp.AgentIdentity{}, fmt.Errorf("charter for %s: %w", agent, err)
	}
	line := startingOneLine
	switch l, err := i.s.OneLine(ctx, agent); {
	case err == nil:
		line = l.Line
	case !errors.Is(err, core.ErrNotFound) && !errors.Is(err, core.ErrUnavailable):
		return supapp.AgentIdentity{}, fmt.Errorf("one-line of %s: %w", agent, err)
	}
	open, err := i.s.Goals(ctx, domain.GoalFilter{Owner: agent, Status: domain.GoalOpen})
	if err != nil {
		return supapp.AgentIdentity{}, fmt.Errorf("goals of %s: %w", agent, err)
	}
	goals := make([]string, 0, len(open))
	for _, g := range open {
		goals = append(goals, g.Title)
	}
	return supapp.AgentIdentity{Agent: a.Core(), Team: chart.Team(agent), Charter: charter.Text, OneLine: line, Goals: goals}, nil
}
