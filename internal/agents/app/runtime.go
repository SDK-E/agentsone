// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/SDK-E/agentsone/internal/agents/domain"
	"github.com/SDK-E/agentsone/internal/contract/common"
	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/runners/sdk/protocol"
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
	// Reason says in plain words why State is failed; empty otherwise.
	Reason string
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
	// No runner starts before the machine recipe is back after a start. A
	// daemon without the machine module has no gate and wakes at once.
	if err := w.s.ports.Startup.Wait(ctx); err != nil && !errors.Is(err, core.ErrUnavailable) {
		return fmt.Errorf("wake %s: wait for the machine: %w", agent, err)
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

// wakeSubject is the subject of the bus message a wake without a task sends.
const wakeSubject = "Wake"

// wakeFor builds the runner contract's wake payload for req. With a task id
// it is that task (reason "task"; the contract's task has no place for the
// reason text, so the text is not sent). A task that does not exist refuses
// the wake with core.ErrNotFound. Without a task it is a bus message from
// the caller to agent, with the subject "Wake" and the reason as the body.
func (s *Society) wakeFor(ctx context.Context, c core.Caller, agent string, req WakeRequest) (core.WakeReason, any, error) {
	if req.TaskID == "" {
		return core.WakeMessage, protocol.BusMessage{
			ID:      common.MessageID(s.ids.New("msg")),
			From:    common.ActorRef(string(c.Kind) + ":" + c.Name),
			To:      common.AgentName(agent),
			Subject: wakeSubject,
			Body:    common.Markdown(req.Reason),
			SentAt:  common.Timestamp(s.clock.Now()),
		}, nil
	}
	t, err := s.ports.Tasks.GetTask(ctx, req.TaskID)
	if err != nil {
		return "", nil, fmt.Errorf("wake %s for task %s: %w", agent, req.TaskID, err)
	}
	task := protocol.Task{
		ID:        common.TaskID(t.ID),
		Title:     common.OneLine(t.Title),
		Body:      common.Markdown(t.Body),
		From:      common.ActorRef(t.CreatedBy),
		CreatedAt: common.Timestamp(t.Created),
	}
	if t.Due != nil {
		d := common.Timestamp(*t.Due)
		task.Due = &d
	}
	return core.WakeTask, task, nil
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
	reason, wake, err := s.wakeFor(ctx, c, agent, req)
	if err != nil {
		return domain.Agent{}, err
	}
	payload, err := json.Marshal(wake)
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
