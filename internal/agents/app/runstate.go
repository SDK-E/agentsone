// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/SDK-E/agentsone/internal/agents/domain"
	"github.com/SDK-E/agentsone/internal/core"
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
	s.runMu.Lock()
	s.failed[agent] = reason
	s.runMu.Unlock()
	if a = s.withRunState(a); a.State == domain.StateFailed {
		s.hub.Publish(agent, StreamOutput, Frame{State: &StateFrame{Agent: agent, State: a.State, Time: s.clock.Now(), Reason: reason}})
	}
	return nil
}

// RunnerRunning takes the runner supervisor's word that agent's runner
// finished its handshake. A failure shown on the agent is cleared and an
// awake state frame without a reason goes out.
func (s *Society) RunnerRunning(_ context.Context, agent string) error {
	s.runMu.Lock()
	_, was := s.failed[agent]
	delete(s.failed, agent)
	s.runMu.Unlock()
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
	s.runMu.Lock()
	_, was := s.failed[agent]
	delete(s.failed, agent)
	s.runMu.Unlock()
	if was {
		s.hub.Publish(agent, StreamOutput, Frame{State: &StateFrame{Agent: agent, State: s.withRunState(a).State, Time: s.clock.Now()}})
	}
	return nil
}

// SetRunState takes the runner supervisor's word on what agent's runner
// does now: waking, awake (connected and idle), working, or sleeping (no
// runner). The record shows it until the next word; it is kept in memory
// only, since the runner is, and a restart starts it again. The state
// frame goes out when what the agent shows changes. state is one of those
// four; any other is core.ErrInvalid.
func (s *Society) SetRunState(ctx context.Context, agent string, state domain.State) error {
	switch state {
	case domain.StateSleeping, domain.StateWaking, domain.StateAwake, domain.StateWorking:
	default:
		return fmt.Errorf("run state %q of %s: %w", state, agent, core.ErrInvalid)
	}
	a, err := s.repo.GetAgent(ctx, agent)
	if err != nil {
		return fmt.Errorf("run state of %s: %w", agent, err)
	}
	before := s.withRunState(a)
	s.runMu.Lock()
	if state == domain.StateSleeping {
		delete(s.live, agent)
	} else {
		s.live[agent] = state
	}
	s.runMu.Unlock()
	if after := s.withRunState(a); after.State != before.State {
		s.hub.Publish(agent, StreamOutput, Frame{State: &StateFrame{Agent: agent, State: after.State, Time: s.clock.Now(), Reason: after.StateReason}})
	}
	return nil
}

// TurnEnded records that a turn of agent ended at at, as the agent's last
// turn. It is stored with the agent, so it survives a restart.
func (s *Society) TurnEnded(ctx context.Context, agent string, at time.Time) error {
	if err := s.repo.RecordTurn(ctx, agent, at.UTC()); err != nil {
		return fmt.Errorf("record the end of a turn of %s: %w", agent, err)
	}
	return nil
}

// withRunState shows a runner failure on a, or else what its runner does.
// Frozen and retired say more, so they win; so does a stored state other
// than sleeping.
func (s *Society) withRunState(a domain.Agent) domain.Agent {
	s.runMu.Lock()
	defer s.runMu.Unlock()
	if reason, ok := s.failed[a.Name]; ok && a.Active() && a.State != domain.StateFrozen {
		a.State, a.StateReason = domain.StateFailed, reason
		return a
	}
	if st, ok := s.live[a.Name]; ok && a.State == domain.StateSleeping {
		a.State = st
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
