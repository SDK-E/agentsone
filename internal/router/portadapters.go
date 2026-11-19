// SPDX-License-Identifier: MIT

package router

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/router/app"
	"github.com/SDK-E/agentsone/internal/router/domain"
)

// This file adapts the daemon's ports to the router's own ports, so the
// router's domain and app never see core types.

const (
	// gatewaySubject is who the gateway sees: the routing call is system
	// work, not one agent's.
	gatewaySubject = "system:router"
	tokenTTL       = 5 * time.Minute
	// routerTemplate is the template id of the default router agent, in the
	// form "<plugin>/<template>". Any template named router counts too.
	routerTemplate = "small-seed/router"
)

// profileAdapter reads the society from the agent profiles port.
type profileAdapter struct{ ports *core.Ports }

func (p profileAdapter) Profiles(ctx context.Context) ([]domain.Profile, error) {
	profiles, err := p.ports.Profiles.Profiles(ctx)
	if err != nil {
		return nil, fmt.Errorf("list agent profiles: %w", err)
	}
	out := make([]domain.Profile, 0, len(profiles))
	for _, a := range profiles {
		out = append(out, domain.Profile{
			Agent: a.Name, DisplayName: a.DisplayName, Role: a.Role, Kind: string(a.Kind),
			State: a.State, Team: a.Team, Router: isRouter(a),
		})
	}
	return out, nil
}

// isRouter marks routers: hired from the router template, or named like one
// for agents hired before the template existed.
func isRouter(a core.AgentProfile) bool {
	return a.Template == routerTemplate || strings.HasSuffix(a.Template, "/router") || a.Name == "router" || strings.HasSuffix(a.Name, "-router")
}

// chatAdapter opens a chat through the chat port.
type chatAdapter struct{ ports *core.Ports }

func (c chatAdapter) OpenChat(ctx context.Context, agent, text, assignmentID string) (app.ChatRef, error) {
	id, err := c.ports.Chat.PostChat(ctx, agent, text, assignmentID)
	if err != nil {
		return app.ChatRef{}, fmt.Errorf("post chat to %s: %w", agent, err)
	}
	return app.ChatRef{Path: "/v1/agents/" + agent + "/chat", MessageID: id}, nil
}

// credentialAdapter issues a short token for a router agent.
type credentialAdapter struct{ ports *core.Ports }

func (c credentialAdapter) TokenFor(ctx context.Context, _ string) (string, error) {
	t, err := c.ports.GatewayTokens.Mint(ctx, core.GatewayGrant{Subject: gatewaySubject, TTL: tokenTTL})
	if err != nil {
		return "", fmt.Errorf("mint gateway token: %w", err)
	}
	return t, nil
}
