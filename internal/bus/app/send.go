// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/SDK-E/agentsone/internal/bus/domain"
	"github.com/SDK-E/agentsone/internal/contract/common"
	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/runners/sdk/protocol"
)

// Post sends a message for an API caller. The sender is the caller; the
// request cannot name another.
func (s *Service) Post(ctx context.Context, d domain.Draft) (domain.Message, error) {
	return s.send(ctx, callerOf(ctx), d)
}

// Send implements core.BusSender for other modules, such as the runner
// protocol's bus.send. With a caller in ctx the message must come from that
// caller, unless the caller is the daemon or a root agent, which may send for
// any agent. With no caller the message's From is trusted.
func (s *Service) Send(ctx context.Context, m core.BusMessage) (core.BusMessage, error) {
	if err := m.Validate(); err != nil {
		return core.BusMessage{}, err
	}
	channel, agent, err := domain.ParseTarget(m.To)
	if err != nil {
		return core.BusMessage{}, err
	}
	c, err := s.senderOf(ctx, m.From)
	if err != nil {
		return core.BusMessage{}, err
	}
	stored, err := s.send(ctx, c, domain.Draft{Channel: channel, To: agent, Text: m.Body})
	if err != nil {
		return core.BusMessage{}, err
	}
	return core.BusMessage{ID: stored.ID, From: stored.From, To: m.To, Body: stored.Text, Sent: stored.CreatedAt}, nil
}

func isSystemActor(actor string) bool {
	return strings.HasPrefix(actor, string(core.CallerSystem)+":")
}

// senderOf decides who sends a message that names its sender.
func (s *Service) senderOf(ctx context.Context, from string) (core.Caller, error) {
	if !domain.ValidActor(from) {
		return core.Caller{}, fmt.Errorf("%w: sender %q is not an actor", core.ErrInvalid, from)
	}
	if c, ok := core.CallerFrom(ctx); ok {
		actor, err := domain.ActorOf(c)
		if err != nil {
			return core.Caller{}, err
		}
		if actor == from {
			return c, nil
		}
		// Only the daemon speaks as the system; a root agent speaks for agents.
		if !isPrivileged(c) || (isSystemActor(from) && c.Kind != core.CallerSystem) {
			return core.Caller{}, fmt.Errorf("%w: %s may not send as %s", core.ErrForbidden, actor, from)
		}
	}
	kind, name, _ := strings.Cut(from, ":")
	c := core.Caller{Kind: core.CallerKind(kind), Name: name}
	if c.Kind != core.CallerAgent {
		return c, nil
	}
	agent, err := s.d.Ports.Agents.GetAgent(ctx, name)
	if errors.Is(err, core.ErrNotFound) {
		return core.Caller{}, fmt.Errorf("%w: sender %s is not an agent", core.ErrInvalid, from)
	}
	if err != nil {
		return core.Caller{}, err
	}
	c.IsRoot = agent.IsRoot
	return c, nil
}

func (s *Service) send(ctx context.Context, c core.Caller, d domain.Draft) (domain.Message, error) {
	actor, err := domain.ActorOf(c)
	if err != nil {
		return domain.Message{}, err
	}
	if c.Kind == core.CallerPlugin {
		return domain.Message{}, fmt.Errorf("%w: plugins cannot send bus messages", core.ErrForbidden)
	}
	if err := d.Validate(); err != nil {
		return domain.Message{}, err
	}
	viewer := s.viewerOf(ctx, c)
	if err := s.checkTarget(ctx, c, viewer, d); err != nil {
		return domain.Message{}, err
	}
	correlation, err := s.correlation(ctx, viewer, d)
	if err != nil {
		return domain.Message{}, err
	}
	if !s.limiter.Allow("send:"+actor, sendRate) {
		return domain.Message{}, core.NewProblem(http.StatusTooManyRequests, "rate-limited", "too many messages sent; slow down")
	}
	id := s.d.IDs.New("msg")
	if correlation == "" {
		correlation = id
	}
	m, err := s.store(ctx, d.Message(id, actor, correlation, s.d.Clock.Now()))
	if err != nil {
		return domain.Message{}, err
	}
	detail := map[string]any{}
	if m.IsDirect() {
		detail["to"] = m.To
		detail["woke"] = s.wake(ctx, m)
	} else {
		detail["channel"] = m.Channel
		if err := s.d.Channels.Touch(ctx, m.Channel, m.CreatedAt); err != nil {
			s.d.Log.WarnContext(ctx, "channel touch failed", "channel", m.Channel, "error", err)
		}
	}
	s.audit(ctx, actor, "bus.send", "message:"+m.ID, detail)
	return m, nil
}

// checkTarget makes sure the recipient or channel exists and the sender may reach it.
func (s *Service) checkTarget(ctx context.Context, c core.Caller, viewer domain.Viewer, d domain.Draft) error {
	if d.To != "" {
		if c.Kind == core.CallerAgent && c.Name == d.To {
			return fmt.Errorf("%w: an agent cannot message itself", core.ErrInvalid)
		}
		if _, err := s.d.Ports.Agents.GetAgent(ctx, d.To); err != nil {
			return fmt.Errorf("recipient %q: %w", d.To, err)
		}
		return nil
	}
	ch, err := s.d.Channels.Get(ctx, d.Channel)
	if err != nil {
		return err
	}
	if !viewer.CanSeeChannel(ch) {
		// A team channel of another branch is reported as missing.
		return fmt.Errorf("channel %q: %w", d.Channel, core.ErrNotFound)
	}
	return nil
}

// correlation returns the conversation id a reply inherits, or "" for a new
// conversation. A message the sender cannot see is not a valid reply target.
func (s *Service) correlation(ctx context.Context, viewer domain.Viewer, d domain.Draft) (string, error) {
	if d.ReplyTo == "" {
		return "", nil
	}
	parent, err := s.d.Messages.Get(ctx, d.ReplyTo)
	if err != nil && !errors.Is(err, core.ErrNotFound) {
		return "", err
	}
	if err != nil || !s.accessFor(viewer).canSee(ctx, parent) {
		return "", fmt.Errorf("%w: reply_to does not name a message you can see", domain.ErrInvalidMessage)
	}
	if parent.CorrelationID != "" {
		return parent.CorrelationID, nil
	}
	return parent.ID, nil
}

// store appends and broadcasts as one step.
func (s *Service) store(ctx context.Context, m domain.Message) (domain.Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	stored, err := s.d.Messages.Append(ctx, m)
	if err != nil {
		return domain.Message{}, err
	}
	s.hub.broadcast(stored)
	return stored, nil
}

// WakeMessage is m as the runner contract's wake payload for reason
// "message". The subject is derived from the text (Message.Subject) and the
// body is the whole text.
func WakeMessage(m domain.Message) protocol.BusMessage {
	out := protocol.BusMessage{
		ID:      common.MessageID(m.ID),
		From:    common.ActorRef(m.From),
		To:      common.AgentName(m.To),
		Subject: common.OneLine(m.Subject()),
		Body:    common.Markdown(m.Text),
		SentAt:  common.Timestamp(m.CreatedAt),
	}
	if m.ReplyTo != "" {
		r := common.MessageID(m.ReplyTo)
		out.ReplyTo = &r
	}
	if m.CorrelationID != "" {
		c := common.AnyID(m.CorrelationID)
		out.CorrelationID = &c
	}
	return out
}

// wake tells the recipient of a direct message about it. A frozen recipient
// stays asleep and a failed wake is logged: the message is stored either
// way, so the recipient finds it when it next looks. It reports whether the
// recipient was woken.
func (s *Service) wake(ctx context.Context, m domain.Message) bool {
	payload, err := json.Marshal(WakeMessage(m))
	if err != nil {
		s.d.Log.WarnContext(ctx, "encode wake payload failed", "message", m.ID, "error", err)
		return false
	}
	err = s.d.Ports.Waker.Wake(ctx, m.To, core.WakeMessage, payload)
	switch {
	case err == nil:
		return true
	case errors.Is(err, core.ErrFrozen):
		s.d.Log.InfoContext(ctx, "recipient is frozen; message stored without a wake", "agent", m.To, "message", m.ID)
	default:
		s.d.Log.WarnContext(ctx, "wake for a bus message failed", "agent", m.To, "message", m.ID, "error", err)
	}
	return false
}
