// SPDX-License-Identifier: MIT

package app

import (
	"context"

	"github.com/SDK-E/agentsone/internal/core"
)

// Profiles is the society's core.AgentProfiles port.
type Profiles struct{ s *Society }

var _ core.AgentProfiles = Profiles{}

// Profiles returns the AgentProfiles port.
func (s *Society) Profiles() Profiles { return Profiles{s: s} }

// Profiles returns every active agent's profile, sorted by name.
func (p Profiles) Profiles(ctx context.Context) ([]core.AgentProfile, error) {
	chart, err := p.s.chart(ctx)
	if err != nil {
		return nil, err
	}
	var out []core.AgentProfile
	for _, a := range chart.Agents() {
		if !a.Active() {
			continue
		}
		out = append(out, core.AgentProfile{
			Name: a.Name, DisplayName: a.DisplayName, Role: a.Role, Kind: core.AgentKind(a.Kind),
			State: string(a.State), Team: chart.Team(a.Name), Template: a.Template,
		})
	}
	return out, nil
}

// ChatPoster is the society's core.ChatPoster port, for the router.
type ChatPoster struct{ s *Society }

var _ core.ChatPoster = ChatPoster{}

// ChatPoster returns the ChatPoster port.
func (s *Society) ChatPoster() ChatPoster { return ChatPoster{s: s} }

// PostChat posts the caller's message, linked to a router assignment, and
// wakes the agent for it.
func (c ChatPoster) PostChat(ctx context.Context, agent, text, assignmentID string) (string, error) {
	m, err := c.s.postChat(ctx, agent, text, assignmentID)
	return m.ID, err
}
