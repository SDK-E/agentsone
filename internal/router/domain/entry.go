// SPDX-License-Identifier: MIT

package domain

import (
	"context"
	"time"
)

// Profile is what the society knows about an agent apart from its line.
type Profile struct {
	Agent       string
	DisplayName string
	Role        string
	Kind        string // always-on, chat or waker
	State       string // sleeping, waking, awake, working, frozen, failed or retired
	Team        string // head of the agent's chart branch
	// Router is true for agents whose template marks them as routers.
	Router bool
}

// Eligible reports whether a request may be routed to the agent: not a
// router, and in a state that can take a chat message.
func (p Profile) Eligible() bool {
	if p.Router {
		return false
	}
	switch p.State {
	case "frozen", "failed", "retired":
		return false
	}
	return true
}

// Entry is one row of the live list: a profile and its latest line, if any.
type Entry struct {
	Profile
	Line    *Line
	Updated time.Time
}

// OneLine returns the line text, or "" when the agent has not emitted one.
func (e Entry) OneLine() string {
	if e.Line == nil {
		return ""
	}
	return e.Line.String()
}

// StoredLine is an agent's last line as kept by a LineRepository.
type StoredLine struct {
	Agent   string
	Line    Line
	Updated time.Time
}

// LineRepository keeps the last line of each agent, so the live list
// survives a restart.
type LineRepository interface {
	// Put sets the agent's line, replacing the previous one.
	Put(ctx context.Context, l StoredLine) error
	// List returns every stored line sorted by agent.
	List(ctx context.Context) ([]StoredLine, error)
	// Delete forgets the agent's line; a missing line is not an error.
	Delete(ctx context.Context, agent string) error
}
