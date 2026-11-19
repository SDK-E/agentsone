// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/SDK-E/agentsone/internal/agents/domain"
	"github.com/SDK-E/agentsone/internal/core"
)

// SeedAgent is one agent a seed creates from an installed template. The
// template gives the name, kind, runner and root flag; the seed adds how
// the agent presents itself.
type SeedAgent struct {
	Template    string
	DisplayName string
	Role        string
}

// SeedPlan is the society a fresh machine starts with: a founder and the
// team it hires first. The founder template's charter becomes the
// society's first charter.
type SeedPlan struct {
	Founder SeedAgent
	Team    []SeedAgent
}

// Seed creates what plan asks for and is not there yet, and returns the
// names it created. It can run again at any time: an agent whose name is
// taken, even by a retired agent, is left alone, and a charter the agents
// already wrote is kept. Only the daemon or the host may seed; the team is
// hired below the founder and the charter written as the founder, audited
// as the seeding caller.
func (s *Society) Seed(ctx context.Context, plan SeedPlan) ([]string, error) {
	c, _ := core.CallerFrom(ctx)
	if c.Kind != core.CallerSystem && c.Kind != core.CallerHost {
		return nil, fmt.Errorf("seed the society: %w", core.ErrForbidden)
	}
	if plan.Founder.Template == "" {
		return nil, fmt.Errorf("%w: the seed has no founder template", domain.ErrInvalidAgent)
	}
	by := string(c.Kind) + ":" + c.Name
	founderTpl, err := s.template(ctx, plan.Founder.Template)
	if err != nil {
		return nil, err
	}
	chart, err := s.chart(ctx)
	if err != nil {
		return nil, err
	}
	var created []string
	founder, ok := chart.Founder()
	if !ok {
		if founder, err = s.Found(ctx, plan.Founder.spec(founderTpl)); err != nil {
			return nil, err
		}
		created = append(created, founder.Name)
	}
	for _, member := range plan.Team {
		tpl, err := s.template(ctx, member.Template)
		if err != nil {
			return created, err
		}
		if chart, err = s.chart(ctx); err != nil {
			return created, err
		}
		if _, err := chart.Get(tpl.Name); err == nil {
			continue
		}
		if _, err := s.hire(ctx, founder.Name, by, member.spec(tpl)); err != nil {
			return created, err
		}
		created = append(created, tpl.Name)
	}
	return created, s.seedCharter(ctx, founder.Name, by, founderTpl)
}

// seedCharter writes the founder template's charter when the society has
// none yet.
func (s *Society) seedCharter(ctx context.Context, founder, by string, tpl core.AgentTemplate) error {
	current, err := s.repo.Charter(ctx)
	if err != nil {
		return fmt.Errorf("read charter: %w", err)
	}
	var seed templateSeed
	if len(tpl.Raw) > 0 {
		if err := json.Unmarshal(tpl.Raw, &seed); err != nil {
			return fmt.Errorf("%w: template %s: %w", domain.ErrInvalidAgent, tpl.ID, err)
		}
	}
	if current.Version > 0 || seed.Charter == "" {
		return nil
	}
	_, err = s.rewriteCharter(ctx, founder, by, 0, seed.Charter)
	return err
}

func (a SeedAgent) spec(tpl core.AgentTemplate) domain.HireSpec {
	return domain.HireSpec{
		Name: tpl.Name, DisplayName: a.DisplayName, Kind: domain.Kind(tpl.Kind), Runner: tpl.Runner,
		Role: a.Role, Template: tpl.ID,
	}
}
