// SPDX-License-Identifier: MIT

package domain

import "context"

// AgentRepository stores agents, retired ones included.
type AgentRepository interface {
	// ListAgents returns every agent, sorted by name.
	ListAgents(ctx context.Context) ([]Agent, error)
	// GetAgent returns ErrAgentNotFound for an unknown name.
	GetAgent(ctx context.Context, name string) (Agent, error)
	// CreateAgent returns ErrNameTaken when the name exists.
	CreateAgent(ctx context.Context, a Agent) error
	// SaveAgents replaces existing agents in one transaction. It returns
	// ErrAgentNotFound, and saves none, when one of them does not exist.
	SaveAgents(ctx context.Context, agents ...Agent) error
}

// CharterRepository stores the one charter.
type CharterRepository interface {
	// Charter returns the zero Charter when none was written.
	Charter(ctx context.Context) (Charter, error)
	// SaveCharter stores c when c.Version is the stored version plus one,
	// and returns ErrStaleVersion otherwise.
	SaveCharter(ctx context.Context, c Charter) error
}

// GoalFilter selects goals. Empty fields match everything.
type GoalFilter struct {
	Owner  string
	Status GoalStatus
	// AfterID returns goals with a greater ID.
	AfterID string
	Limit   int
}

// GoalRepository stores goals.
type GoalRepository interface {
	CreateGoal(ctx context.Context, g Goal) error
	// GetGoal returns ErrGoalNotFound for an unknown id.
	GetGoal(ctx context.Context, id string) (Goal, error)
	// SaveGoal returns ErrGoalNotFound for an unknown id.
	SaveGoal(ctx context.Context, g Goal) error
	// DeleteGoal returns ErrGoalNotFound for an unknown id.
	DeleteGoal(ctx context.Context, id string) error
	// ListGoals returns matching goals sorted by ID.
	ListGoals(ctx context.Context, f GoalFilter) ([]Goal, error)
}

// BudgetRepository stores the pushed pool, the allocations and usage.
type BudgetRepository interface {
	// Pool returns a Pool with Known false before the first push.
	Pool(ctx context.Context) (Pool, error)
	SavePool(ctx context.Context, p Pool) error
	Allocations(ctx context.Context) (map[string]int64, error)
	// SetAllocation stores amount for agent; nil removes it.
	SetAllocation(ctx context.Context, agent string, amount *int64) error
	Usage(ctx context.Context) (map[string]int64, error)
	// AddUsage adds micros to agent's recorded spending.
	AddUsage(ctx context.Context, agent string, micros int64) error
}

// ChatRepository stores chat messages.
type ChatRepository interface {
	AppendChat(ctx context.Context, m ChatMessage) error
	// ListChat returns up to limit messages of agent's chat, newest first,
	// with an ID below beforeID when it is not empty.
	ListChat(ctx context.Context, agent, beforeID string, limit int) ([]ChatMessage, error)
}

// Repository is every store of the society context. Every implementation
// passes agentstest.RunRepositoryContract.
type Repository interface {
	AgentRepository
	CharterRepository
	GoalRepository
	BudgetRepository
	ChatRepository
}
