// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/SDK-E/agentsone/internal/agents/domain"
	"github.com/SDK-E/agentsone/internal/contract/common"
	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/runners/sdk/protocol"
)

// OutputChunk is one piece of runner output, as the supervisor hands it on.
// A chunk with ChatID answers that chat message; the chunks of one turn are
// joined into one agent reply when the Final chunk arrives.
type OutputChunk struct {
	TurnID string
	ChatID string
	Index  int
	Text   string
	Final  bool
}

type reply struct {
	id   string
	text strings.Builder
}

// chatWake is the chat wake's payload: the runner contract's ChatMessage
// (contract/schemas/runner/messages.schema.json), which the runner checks
// strictly. The stored message's id (cht_) is the chat the runner answers
// in; the runner's message id is the same ULID with the msg_ prefix. A
// message a router agent posted for an assignment names that router.
func chatWake(m domain.ChatMessage, c core.Caller) protocol.ChatMessage {
	w := protocol.ChatMessage{
		ID:     common.MessageID("msg_" + strings.TrimPrefix(m.ID, "cht_")),
		ChatID: common.ChatID(m.ID), From: common.ActorRef(m.Author),
		Text: common.Markdown(m.Text), SentAt: common.Timestamp(m.CreatedAt),
	}
	if m.AssignmentID != "" && c.Kind == core.CallerAgent {
		router := common.AgentName(c.Name)
		w.RoutedBy = &router
	}
	return w
}

// SendChat posts the caller's message into agent's chat and wakes the
// agent for it. Humans may chat with any agent. A frozen agent (423) or one
// out of budget (402) gets nothing stored.
func (s *Society) SendChat(ctx context.Context, agent, text string) (domain.ChatMessage, error) {
	return s.postChat(ctx, agent, text, "")
}

func (s *Society) postChat(ctx context.Context, agent, text, assignmentID string) (domain.ChatMessage, error) {
	c, ok := core.CallerFrom(ctx)
	if !ok {
		return domain.ChatMessage{}, fmt.Errorf("no caller: %w", core.ErrUnauthorized)
	}
	if err := s.canWake(ctx, agent); err != nil {
		return domain.ChatMessage{}, err
	}
	author := string(c.Kind) + ":" + c.Name
	m, err := domain.NewChatMessage(s.ids.New("cht"), agent, domain.ChatRoleFor(string(c.Kind)), author, text, s.clock.Now())
	if err != nil {
		return domain.ChatMessage{}, err
	}
	m.AssignmentID = assignmentID
	if err := s.repo.AppendChat(ctx, m); err != nil {
		return domain.ChatMessage{}, fmt.Errorf("store chat message: %w", err)
	}
	s.hub.Publish(agent, StreamChat, Frame{Message: &ChatFrame{Message: m}})
	payload, err := json.Marshal(chatWake(m, c))
	if err != nil {
		return domain.ChatMessage{}, fmt.Errorf("encode chat wake: %w", err)
	}
	if err := s.rt.Wake(ctx, agent, core.WakeChat, payload); err != nil {
		return domain.ChatMessage{}, fmt.Errorf("wake %s for chat: %w", agent, err)
	}
	return m, nil
}

// Chat returns up to limit messages of agent's chat, newest first, before
// the message beforeID when it is set.
func (s *Society) Chat(ctx context.Context, agent, beforeID string, limit int) ([]domain.ChatMessage, error) {
	if _, err := s.repo.GetAgent(ctx, agent); err != nil {
		return nil, err
	}
	return s.repo.ListChat(ctx, agent, beforeID, limit)
}

// Output takes runner output from the supervisor (it is the supervisor's
// OutputSink). Chat output streams as deltas and is stored as the agent's
// reply on the final chunk; other output goes to the output stream.
func (s *Society) Output(ctx context.Context, agent string, chunk OutputChunk) error {
	if chunk.ChatID == "" {
		s.hub.Publish(agent, StreamOutput, Frame{Output: &OutputFrame{Agent: agent, Stream: "text", Text: chunk.Text, Time: s.clock.Now()}})
		return nil
	}
	s.mu.Lock()
	r, ok := s.replies[chunk.TurnID]
	if !ok {
		r = &reply{id: s.ids.New("cht")}
		s.replies[chunk.TurnID] = r
	}
	r.text.WriteString(chunk.Text)
	if chunk.Final {
		delete(s.replies, chunk.TurnID)
	}
	s.mu.Unlock()
	if chunk.Text != "" {
		s.hub.Publish(agent, StreamChat, Frame{Delta: &DeltaFrame{MessageID: r.id, Text: chunk.Text}})
	}
	if !chunk.Final {
		return nil
	}
	m, err := domain.NewChatMessage(r.id, agent, domain.ChatAgent, "agent:"+agent, r.text.String(), s.clock.Now())
	if err != nil {
		return err
	}
	if err := s.repo.AppendChat(ctx, m); err != nil {
		return fmt.Errorf("store reply of %s: %w", agent, err)
	}
	s.hub.Publish(agent, StreamChat, Frame{Message: &ChatFrame{Message: m}})
	return nil
}
