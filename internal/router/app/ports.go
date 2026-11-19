// SPDX-License-Identifier: MIT

package app

import (
	"context"

	"github.com/SDK-E/agentsone/internal/router/domain"
)

// Profiles lists the agents of the society with what the live list shows
// besides the line. The module fills it from the agents context's port.
type Profiles interface {
	Profiles(ctx context.Context) ([]domain.Profile, error)
}

// ChatRef says where an opened chat message went.
type ChatRef struct {
	Path      string // API path of the agent's chat, for example /v1/agents/builder/chat
	MessageID string
}

// Chats posts a human request into an agent's chat and wakes the agent.
// A frozen agent answers core.ErrFrozen and one out of budget core.ErrBudget.
type Chats interface {
	OpenChat(ctx context.Context, agent, text, assignmentID string) (ChatRef, error)
}

// Credentials gives the model credential of a router agent, so the cost of
// a routing call lands on that router's budget.
type Credentials interface {
	TokenFor(ctx context.Context, router string) (string, error)
}

// Prompt is one quick model call.
type Prompt struct {
	System string
	User   string
}

// Gateway makes one quick model call through the edge gateway with the
// router's credential and returns the model's text. An exhausted budget is
// core.ErrBudget.
type Gateway interface {
	Complete(ctx context.Context, token string, p Prompt) (string, error)
}
