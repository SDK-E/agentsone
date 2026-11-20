// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"strings"

	"github.com/SDK-E/agentsone/internal/agents/domain"
)

// maxStateReason is the contract's limit on state_reason.
const maxStateReason = 500

// defaultStateReason stands in for a blank reason.
const defaultStateReason = "The runner stopped with an error."

// RunnerFailed takes the runner supervisor's word that agent's runner
// cannot start or stopped with an error. The agent shows as failed with
// reason until RunnerRunning. The failure is kept in memory only: it
// belongs to the runner, which a restart replaces, so core.db never holds
// it and the router's live list is not changed.
func (s *Society) RunnerFailed(ctx context.Context, agent, reason string) error {
	a, err := s.repo.GetAgent(ctx, agent)
	if err != nil {
		return err
	}
	reason = cleanReason(reason)
	s.mu.Lock()
	s.failed[agent] = reason
	s.mu.Unlock()
	if a = s.withRunState(a); a.State == domain.StateFailed {
		s.hub.Publish(agent, StreamOutput, Frame{State: &StateFrame{Agent: agent, State: a.State, Time: s.clock.Now(), Reason: reason}})
	}
	return nil
}

// RunnerRunning takes the runner supervisor's word that agent's runner
// finished its handshake. A failure shown on the agent is cleared and an
// awake state frame without a reason goes out.
func (s *Society) RunnerRunning(_ context.Context, agent string) error {
	s.mu.Lock()
	_, was := s.failed[agent]
	delete(s.failed, agent)
	s.mu.Unlock()
	if was {
		s.hub.Publish(agent, StreamOutput, Frame{State: &StateFrame{Agent: agent, State: domain.StateAwake, Time: s.clock.Now()}})
	}
	return nil
}

// RunnerCleared takes the runner supervisor's word that what kept agent's
// runner off is gone (the network wall holds again), though the runner has
// not started. A failure shown on the agent is cleared and a state frame
// with the agent's own state, without a reason, goes out.
func (s *Society) RunnerCleared(ctx context.Context, agent string) error {
	a, err := s.repo.GetAgent(ctx, agent)
	if err != nil {
		return err
	}
	s.mu.Lock()
	_, was := s.failed[agent]
	delete(s.failed, agent)
	s.mu.Unlock()
	if was {
		s.hub.Publish(agent, StreamOutput, Frame{State: &StateFrame{Agent: agent, State: s.withRunState(a).State, Time: s.clock.Now()}})
	}
	return nil
}

// withRunState shows a runner failure on a. Frozen and retired say more,
// so they win.
func (s *Society) withRunState(a domain.Agent) domain.Agent {
	s.mu.Lock()
	reason, ok := s.failed[a.Name]
	s.mu.Unlock()
	if ok && a.Active() && a.State != domain.StateFrozen {
		a.State, a.StateReason = domain.StateFailed, reason
	}
	return a
}

// withRunStates is withRunState over a whole chart.
func (s *Society) withRunStates(c domain.Chart) domain.Chart {
	all := c.Agents()
	for i := range all {
		all[i] = s.withRunState(all[i])
	}
	return domain.NewChart(all)
}

// cleanReason makes reason one line of at most maxStateReason characters.
func cleanReason(reason string) string {
	reason = strings.Join(strings.Fields(reason), " ")
	if reason == "" {
		return defaultStateReason
	}
	if r := []rune(reason); len(r) > maxStateReason {
		reason = strings.TrimSpace(string(r[:maxStateReason]))
	}
	return reason
}
