// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/SDK-E/agentsone/internal/agents/domain"
	"github.com/SDK-E/agentsone/internal/core"
)

// ChatFrame carries a stored chat message.
type ChatFrame struct{ Message domain.ChatMessage }

// DeltaFrame is partial reply text, joined by MessageID.
type DeltaFrame struct {
	MessageID string
	Text      string
}

// OutputFrame is a chunk of runner output that is not chat.
type OutputFrame struct {
	Agent  string
	Stream string
	Text   string
	Time   time.Time
}

// StateFrame is an agent state change.
type StateFrame struct {
	Agent string
	State domain.State
	Time  time.Time
}

// Waker is the society's core.Waker port: it refuses retired, frozen and
// out-of-budget agents, then hands the wake to the runner supervisor.
type Waker struct{ s *Society }

var _ core.Waker = Waker{}

// Waker returns the Waker port.
func (s *Society) Waker() Waker { return Waker{s: s} }

// Wake wakes agent for reason.
func (w Waker) Wake(ctx context.Context, agent string, reason core.WakeReason, payload json.RawMessage) error {
	if err := w.s.canWake(ctx, agent); err != nil {
		return err
	}
	if err := w.s.rt.Wake(ctx, agent, reason, payload); err != nil {
		return fmt.Errorf("wake %s: %w", agent, err)
	}
	return nil
}

// canWake refuses a retired or frozen agent, or one whose soft budget share
// is used up.
func (s *Society) canWake(ctx context.Context, agent string) error {
	split, err := s.split(ctx)
	if err != nil {
		return err
	}
	a, err := s.repo.GetAgent(ctx, agent)
	if err != nil {
		return err
	}
	switch a.State {
	case domain.StateRetired:
		return fmt.Errorf("%w: %s", domain.ErrRetired, agent)
	case domain.StateFrozen:
		return fmt.Errorf("%s is frozen: %w", agent, core.ErrFrozen)
	}
	return split.CanWake(agent)
}

// WakeRequest is the wakeAgent operation's input.
type WakeRequest struct {
	Reason string
	TaskID string
}

type wakePayload struct {
	Reason string `json:"reason"`
	TaskID string `json:"task_id,omitempty"`
	By     string `json:"by"`
}

const maxWakeReason = 500

// WakeAgent wakes agent for the calling agent or daemon part. Humans enter
// through chat and the router instead, so they may not nudge agents directly.
func (s *Society) WakeAgent(ctx context.Context, agent string, req WakeRequest) (domain.Agent, error) {
	c, ok := core.CallerFrom(ctx)
	if !ok {
		return domain.Agent{}, fmt.Errorf("no caller: %w", core.ErrUnauthorized)
	}
	if c.Kind != core.CallerAgent && c.Kind != core.CallerSystem {
		return domain.Agent{}, fmt.Errorf("wake %s: humans talk to agents through chat: %w", agent, core.ErrForbidden)
	}
	if n := len([]rune(req.Reason)); n < 1 || n > maxWakeReason {
		return domain.Agent{}, fmt.Errorf("wake reason has %d characters, want 1..%d: %w", n, maxWakeReason, core.ErrInvalid)
	}
	reason := core.WakeMessage
	if req.TaskID != "" {
		reason = core.WakeTask
	}
	payload, err := json.Marshal(wakePayload{Reason: req.Reason, TaskID: req.TaskID, By: string(c.Kind) + ":" + c.Name})
	if err != nil {
		return domain.Agent{}, fmt.Errorf("encode wake: %w", err)
	}
	if err := s.Waker().Wake(ctx, agent, reason, payload); err != nil {
		return domain.Agent{}, err
	}
	return s.repo.GetAgent(ctx, agent)
}

// Sleep puts agent to sleep. An agent may put itself or an agent below it
// to sleep.
func (s *Society) Sleep(ctx context.Context, agent string, compact bool) (domain.Agent, error) {
	name, err := actor(ctx)
	if err != nil {
		return domain.Agent{}, err
	}
	chart, err := s.chart(ctx)
	if err != nil {
		return domain.Agent{}, err
	}
	a, err := chart.Get(agent)
	if err != nil {
		return domain.Agent{}, err
	}
	if name != agent {
		if err := chart.CanManage(name, agent); err != nil {
			return domain.Agent{}, err
		}
	}
	if err := s.rt.Sleep(ctx, agent, compact); err != nil {
		return domain.Agent{}, fmt.Errorf("sleep %s: %w", agent, err)
	}
	return a, nil
}

// Freeze freezes a scope: "agent:<name>", "team:<head>" or "machine". A
// frozen agent is not woken and its runner sleeps until Release. This is
// the soft half of the host's freeze lever; the hard half is on the host.
func (s *Society) Freeze(ctx context.Context, scope string) error {
	return s.setFrozen(ctx, scope, true)
}

// Release lifts a freeze from a scope.
func (s *Society) Release(ctx context.Context, scope string) error {
	return s.setFrozen(ctx, scope, false)
}

func (s *Society) setFrozen(ctx context.Context, scope string, frozen bool) error {
	c, _ := core.CallerFrom(ctx)
	if c.Kind != core.CallerHost && c.Kind != core.CallerSystem {
		return fmt.Errorf("freeze is a host lever: %w", core.ErrForbidden)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	chart, err := s.chart(ctx)
	if err != nil {
		return err
	}
	targets, err := scopeAgents(chart, scope)
	if err != nil {
		return err
	}
	var changed []domain.Agent
	for _, a := range targets {
		if frozen {
			err = s.rt.Freeze(ctx, a.Name)
			a.State = domain.StateFrozen
		} else {
			err = s.rt.Release(ctx, a.Name)
			a.State = domain.StateSleeping
		}
		if err != nil {
			return fmt.Errorf("freeze %s: %w", a.Name, err)
		}
		changed = append(changed, a)
		s.hub.Publish(a.Name, StreamOutput, Frame{State: &StateFrame{Agent: a.Name, State: a.State, Time: s.clock.Now()}})
	}
	if err := s.repo.SaveAgents(ctx, changed...); err != nil {
		return fmt.Errorf("save freeze: %w", err)
	}
	action := "agent.freeze"
	if !frozen {
		action = "agent.release"
	}
	raw, err := json.Marshal(map[string]any{"scope": scope, "agents": len(changed)})
	if err != nil {
		return fmt.Errorf("encode %s: %w", action, err)
	}
	return s.ports.Audit.Append(ctx, core.AuditEntry{Actor: string(c.Kind) + ":" + c.Name, Action: action, Target: scope, Detail: raw})
}

// scopeAgents returns the active agents a freeze scope names.
func scopeAgents(chart domain.Chart, scope string) ([]domain.Agent, error) {
	kind, name, _ := strings.Cut(scope, ":")
	var out []domain.Agent
	for _, a := range chart.Agents() {
		if !a.Active() {
			continue
		}
		switch {
		case kind == "machine" && name == "",
			kind == "agent" && a.Name == name,
			kind == "team" && chart.Team(a.Name) == name:
			out = append(out, a)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("freeze scope %q names no agent: %w", scope, core.ErrNotFound)
	}
	return out, nil
}

// OneLine returns the agent's live list entry.
func (s *Society) OneLine(ctx context.Context, agent string) (core.LiveLine, error) {
	if _, err := s.repo.GetAgent(ctx, agent); err != nil {
		return core.LiveLine{}, err
	}
	lines, err := s.ports.Live.Lines(ctx)
	if err != nil {
		return core.LiveLine{}, fmt.Errorf("read live list: %w", err)
	}
	for _, l := range lines {
		if l.Agent == agent {
			return l, nil
		}
	}
	return core.LiveLine{}, fmt.Errorf("%s has no one-line yet: %w", agent, core.ErrNotFound)
}

// SetOneLine sets the calling agent's own one-line. Nobody else may.
func (s *Society) SetOneLine(ctx context.Context, agent, text string) (core.LiveLine, error) {
	name, err := actor(ctx)
	if err != nil {
		return core.LiveLine{}, err
	}
	if _, err := s.repo.GetAgent(ctx, agent); err != nil {
		return core.LiveLine{}, err
	}
	if name != agent {
		return core.LiveLine{}, fmt.Errorf("only %s sets its one-line: %w", agent, core.ErrForbidden)
	}
	if err := s.ports.Live.UpdateLine(ctx, agent, text); err != nil {
		return core.LiveLine{}, err
	}
	return s.OneLine(ctx, agent)
}
