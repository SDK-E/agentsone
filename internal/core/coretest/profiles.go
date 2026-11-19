// SPDX-License-Identifier: MIT

package coretest

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/SDK-E/agentsone/internal/core"
)

// Profiles is an in-memory core.AgentProfiles.
type Profiles struct {
	mu     sync.Mutex
	byName map[string]core.AgentProfile
}

var _ core.AgentProfiles = (*Profiles)(nil)

// NewProfiles returns an empty profile list.
func NewProfiles() *Profiles { return &Profiles{byName: map[string]core.AgentProfile{}} }

// Add stores p, replacing any profile with the same name.
func (p *Profiles) Add(profile core.AgentProfile) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.byName[profile.Name] = profile
}

// Profiles returns every profile sorted by name.
func (p *Profiles) Profiles(context.Context) ([]core.AgentProfile, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]core.AgentProfile, 0, len(p.byName))
	for _, profile := range p.byName {
		out = append(out, profile)
	}
	slices.SortFunc(out, func(a, b core.AgentProfile) int { return strings.Compare(a.Name, b.Name) })
	return out, nil
}

// ChatPost is one recorded PostChat call.
type ChatPost struct {
	ID           string
	Agent        string
	Text         string
	AssignmentID string
	// Author is the caller as "<kind>:<name>".
	Author string
}

// Chat is a core.ChatPoster that records posts instead of storing chat.
type Chat struct {
	mu    sync.Mutex
	ids   core.IDs
	posts []ChatPost
}

var _ core.ChatPoster = (*Chat)(nil)

// NewChat returns a poster that gives messages "cht" ids from ids.
func NewChat(ids core.IDs) *Chat { return &Chat{ids: ids} }

// PostChat records the post, refusing empty text like the real one.
func (c *Chat) PostChat(ctx context.Context, agent, text, assignmentID string) (string, error) {
	if text == "" {
		return "", fmt.Errorf("chat message needs text: %w", core.ErrInvalid)
	}
	caller, _ := core.CallerFrom(ctx)
	c.mu.Lock()
	defer c.mu.Unlock()
	post := ChatPost{ID: c.ids.New("cht"), Agent: agent, Text: text, AssignmentID: assignmentID, Author: string(caller.Kind) + ":" + caller.Name}
	c.posts = append(c.posts, post)
	return post.ID, nil
}

// Posts returns recorded posts in order.
func (c *Chat) Posts() []ChatPost {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]ChatPost(nil), c.posts...)
}
