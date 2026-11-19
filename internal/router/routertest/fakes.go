// SPDX-License-Identifier: MIT

// Package routertest holds the router's hand-written fakes and the
// repository contract suite, shared by the app, adapter and module tests.
package routertest

import (
	"context"
	"fmt"
	"sync"

	"github.com/SDK-E/agentsone/internal/router/app"
	"github.com/SDK-E/agentsone/internal/router/domain"
)

// Profiles is a fixed list of profiles.
type Profiles struct {
	mu   sync.Mutex
	list []domain.Profile
	Err  error
}

var _ app.Profiles = (*Profiles)(nil)

// NewProfiles returns a fake society.
func NewProfiles(list ...domain.Profile) *Profiles { return &Profiles{list: list} }

// Set replaces the society.
func (p *Profiles) Set(list ...domain.Profile) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.list = list
}

// Profiles returns a copy of the list, or Err.
func (p *Profiles) Profiles(context.Context) ([]domain.Profile, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.Err != nil {
		return nil, p.Err
	}
	return append([]domain.Profile(nil), p.list...), nil
}

// Agent builds a profile for a plain agent that is awake.
func Agent(name, role string) domain.Profile {
	return domain.Profile{Agent: name, DisplayName: name, Role: role, Kind: "chat", State: "awake", Team: "founder"}
}

// Router builds a profile for a router agent.
func Router(name string) domain.Profile {
	p := Agent(name, "Router")
	p.Router = true
	return p
}

// Posted is one chat message the fake chats received.
type Posted struct{ Agent, Text, AssignmentID string }

// Chats records posts; agents named in Refuse answer with their error.
type Chats struct {
	mu     sync.Mutex
	Posts  []Posted
	Refuse map[string]error
}

var _ app.Chats = (*Chats)(nil)

// NewChats returns chats that accept everything.
func NewChats() *Chats { return &Chats{Refuse: map[string]error{}} }

// OpenChat records the post.
func (c *Chats) OpenChat(_ context.Context, agent, text, assignmentID string) (app.ChatRef, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err, ok := c.Refuse[agent]; ok {
		return app.ChatRef{}, err
	}
	c.Posts = append(c.Posts, Posted{Agent: agent, Text: text, AssignmentID: assignmentID})
	n := len(c.Posts)
	return app.ChatRef{Path: "/v1/agents/" + agent + "/chat", MessageID: fmt.Sprintf("cht_%026d", n)}, nil
}

// Credentials gives each router a fixed token, or an error.
type Credentials struct{ Err error }

var _ app.Credentials = Credentials{}

// TokenFor returns "token-of-<router>".
func (c Credentials) TokenFor(_ context.Context, router string) (string, error) {
	if c.Err != nil {
		return "", c.Err
	}
	return "token-of-" + router, nil
}

// Call is one model call the fake gateway received.
type Call struct {
	Token  string
	Prompt app.Prompt
}

// Gateway answers model calls from a queue of replies; it never calls a
// real provider.
type Gateway struct {
	mu      sync.Mutex
	Calls   []Call
	replies []Reply
}

// Reply is one canned gateway answer.
type Reply struct {
	Text string
	Err  error
}

var _ app.Gateway = (*Gateway)(nil)

// NewGateway returns a gateway that gives replies in order; after the last
// one it repeats it.
func NewGateway(replies ...Reply) *Gateway { return &Gateway{replies: replies} }

// Complete records the call and gives the next reply.
func (g *Gateway) Complete(_ context.Context, token string, p app.Prompt) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.Calls = append(g.Calls, Call{Token: token, Prompt: p})
	if len(g.replies) == 0 {
		return "", fmt.Errorf("fake gateway has no reply")
	}
	r := g.replies[min(len(g.Calls), len(g.replies))-1]
	return r.Text, r.Err
}

// CallCount returns how many calls arrived.
func (g *Gateway) CallCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.Calls)
}
