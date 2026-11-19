// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/memory/domain"
)

// principal turns the caller on ctx into who acts on memory. An agent's team
// is the head of its chart branch, read from the agent directory.
func (s *Service) principal(ctx context.Context) (domain.Principal, error) {
	c, ok := core.CallerFrom(ctx)
	if !ok || c.Name == "" {
		return domain.Principal{}, fmt.Errorf("memory: %w", core.ErrUnauthorized)
	}
	p := domain.Principal{Name: c.Name, Root: c.IsRoot}
	switch c.Kind {
	case core.CallerAgent:
		p.Kind = domain.KindAgent
		team, err := s.ports.Agents.TeamRoot(ctx, c.Name)
		if errors.Is(err, core.ErrNotFound) {
			return domain.Principal{}, fmt.Errorf("memory: agent %s is not in the chart: %w", c.Name, core.ErrUnauthorized)
		}
		if err != nil {
			return domain.Principal{}, fmt.Errorf("memory: team of %s: %w", c.Name, err)
		}
		p.Team = team
	case core.CallerHuman:
		p.Kind = domain.KindHuman
	case core.CallerSystem, core.CallerHost:
		p.Kind = domain.KindSystem
	case core.CallerPlugin:
		p.Kind = domain.KindPlugin
	default:
		return domain.Principal{}, fmt.Errorf("memory: caller kind %q: %w", c.Kind, core.ErrUnauthorized)
	}
	return p, nil
}
