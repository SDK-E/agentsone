// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/SDK-E/agentsone/internal/agents/supervisor/domain"
	"github.com/SDK-E/agentsone/internal/contract/eventdata"
	"github.com/SDK-E/agentsone/internal/core"
)

// ActionBudgetResume is the audit action for an agent whose paused turn was
// resumed because its budget is back, on the target agent:<name>.
const ActionBudgetResume = "agent.budget.resume"

// resumeWakeTimeout is how long a resume wake waits for the runner's answer.
const resumeWakeTimeout = 30 * time.Second

// resumeBudget resumes every agent that paused a turn for budget and may
// spend again (rule R2). A budget that is raised, reset or renewed shows as
// remaining budget in the agent's share. For each such agent it publishes
// agentsone.agents.budget.restored and wakes the agent with that event; the
// runner then resumes the paused turn. It writes one audit record per
// resumed agent. An agent that is not paused is left alone, and one paused
// turn is resumed once: until the agent's budget is seen empty again, or
// its turn is no longer paused, it is not resumed a second time.
func (s *Supervisor) resumeBudget(ctx context.Context) {
	type paused struct{ agent, turn string }
	var candidates []paused
	s.mu.Lock()
	for name, r := range s.runners {
		turn := r.session.PausedTurn()
		if turn == "" {
			delete(s.resumed, name)
			continue
		}
		switch r.session.State() {
		case domain.StateStarting, domain.StateWelcomed, domain.StateWorking:
			continue // a runner on its way up or at work takes no wake for it yet
		}
		candidates = append(candidates, paused{name, turn})
	}
	s.mu.Unlock()
	slices.SortFunc(candidates, func(a, b paused) int { return compareStrings(a.agent, b.agent) })
	for _, c := range candidates {
		s.resumeOne(ctx, c.agent, c.turn)
	}
}

func compareStrings(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

func (s *Supervisor) resumeOne(ctx context.Context, agent, turn string) {
	b, err := s.d.Ports.Budget.Allocation(ctx, agent)
	if err != nil {
		return // no share to read: nothing says the budget is back
	}
	s.mu.Lock()
	if b.RemainingMicros <= 0 {
		delete(s.resumed, agent) // empty again: the next restore resumes it again
		s.mu.Unlock()
		return
	}
	if s.resumed[agent] == turn {
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()
	if _, ok := s.wallRefusal(); !ok {
		return
	}
	if frozen, err := s.Frozen(ctx, agent); err != nil || frozen {
		return // resumed once it is released
	}

	s.mu.Lock()
	s.resumed[agent] = turn
	s.mu.Unlock()
	data := eventdata.AgentsBudgetRestored{Agent: agent}
	if turn != "" {
		data.PausedTurnID = &turn
	}
	ev, err := core.PublishData(ctx, s.d.Ports.Events, core.Publication{
		Source: "/agents/supervisor", Actor: "system:supervisor", ChartScope: "agent:" + agent, Data: data,
	})
	if err != nil {
		s.forgetResume(agent)
		s.log().Warn("supervisor: cannot publish that the budget is back", "agent", agent, "err", err)
		return
	}
	s.audit(ctx, ActionBudgetResume, "agent:"+agent, map[string]any{"pausedTurn": turn, "remainingMicros": b.RemainingMicros})
	payload, err := json.Marshal(ev)
	if err != nil {
		s.log().Error("supervisor: cannot encode the budget wake", "agent", agent, "err", err)
		return
	}
	// A wake waits for the runner's answer, and Tick must go on checking
	// heartbeats meanwhile, so the wake goes out from another goroutine.
	go func() {
		wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), resumeWakeTimeout)
		defer cancel()
		if err := s.Wake(wctx, agent, core.WakeEvent, payload); err != nil {
			if errors.Is(err, core.ErrFrozen) {
				s.forgetResume(agent)
				return
			}
			s.log().Warn("supervisor: the agent did not take the wake that resumes its paused turn", "agent", agent, "err", err)
		}
	}()
}

func (s *Supervisor) forgetResume(agent string) {
	s.mu.Lock()
	delete(s.resumed, agent)
	s.mu.Unlock()
}
