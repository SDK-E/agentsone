// SPDX-License-Identifier: MIT

package domain

import (
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/SDK-E/agentsone/internal/core"
)

// ErrInvalidChat is a chat message the contract does not allow.
var ErrInvalidChat = fmt.Errorf("invalid chat message: %w", core.ErrInvalid)

// ChatRole says who wrote a chat message.
type ChatRole string

// Chat roles.
const (
	ChatHuman  ChatRole = "human"
	ChatAgent  ChatRole = "agent"
	ChatSystem ChatRole = "system"
)

// ChatRoleFor maps a caller kind to the role of the message it writes.
func ChatRoleFor(callerKind string) ChatRole {
	switch callerKind {
	case "human":
		return ChatHuman
	case "agent":
		return ChatAgent
	}
	return ChatSystem
}

const maxChatText = 32000

// ChatMessage is one message in an agent's chat.
type ChatMessage struct {
	ID           string
	Agent        string
	Role         ChatRole
	Author       string
	Text         string
	AssignmentID string
	CreatedAt    time.Time
}

// NewChatMessage returns a message for agent's chat.
func NewChatMessage(id, agent string, role ChatRole, author, text string, at time.Time) (ChatMessage, error) {
	if n := utf8.RuneCountInString(text); n < 1 || n > maxChatText {
		return ChatMessage{}, fmt.Errorf("%w: %d characters, want 1..%d", ErrInvalidChat, n, maxChatText)
	}
	return ChatMessage{ID: id, Agent: agent, Role: role, Author: author, Text: text, CreatedAt: at}, nil
}
