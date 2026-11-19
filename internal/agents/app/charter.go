// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/SDK-E/agentsone/internal/agents/domain"
	"github.com/SDK-E/agentsone/internal/core"
)

// Charter returns the society's charter; version 0 means none yet.
func (s *Society) Charter(ctx context.Context) (domain.Charter, error) {
	return s.repo.Charter(ctx)
}

// RewriteCharter rewrites the charter for the calling root agent.
func (s *Society) RewriteCharter(ctx context.Context, expected int, text string) (domain.Charter, error) {
	name, err := actor(ctx)
	if err != nil {
		return domain.Charter{}, err
	}
	return s.rewriteCharter(ctx, name, "agent:"+name, expected, text)
}

// rewriteCharter rewrites the charter as root agent name. by is who acts,
// as audited: the agent itself, or the daemon seeding the society.
func (s *Society) rewriteCharter(ctx context.Context, name, by string, expected int, text string) (domain.Charter, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	chart, err := s.chart(ctx)
	if err != nil {
		return domain.Charter{}, err
	}
	current, err := s.repo.Charter(ctx)
	if err != nil {
		return domain.Charter{}, fmt.Errorf("read charter: %w", err)
	}
	next, err := current.Rewrite(chart, name, expected, text, s.clock.Now())
	if err != nil {
		return domain.Charter{}, err
	}
	if err := s.repo.SaveCharter(ctx, next); err != nil {
		return domain.Charter{}, fmt.Errorf("save charter: %w", err)
	}
	raw, err := json.Marshal(map[string]int{"version": next.Version, "characters": len([]rune(next.Text))})
	if err != nil {
		return domain.Charter{}, fmt.Errorf("encode charter detail: %w", err)
	}
	if err := s.ports.Audit.Append(ctx, core.AuditEntry{Actor: by, Action: "charter.rewrite", Target: "charter:society", Detail: raw}); err != nil {
		return domain.Charter{}, fmt.Errorf("audit charter.rewrite: %w", err)
	}
	return next, nil
}

// Goals returns matching goals sorted by id. Every caller may read them.
func (s *Society) Goals(ctx context.Context, f domain.GoalFilter) ([]domain.Goal, error) {
	return s.repo.ListGoals(ctx, f)
}

// Goal returns one goal.
func (s *Society) Goal(ctx context.Context, id string) (domain.Goal, error) {
	return s.repo.GetGoal(ctx, id)
}

// SetGoal sets a goal for the calling agent or an agent below it.
func (s *Society) SetGoal(ctx context.Context, spec domain.GoalSpec) (domain.Goal, error) {
	name, err := actor(ctx)
	if err != nil {
		return domain.Goal{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	chart, err := s.chart(ctx)
	if err != nil {
		return domain.Goal{}, err
	}
	var parent *domain.Goal
	if spec.ParentID != "" {
		p, err := s.repo.GetGoal(ctx, spec.ParentID)
		if errors.Is(err, domain.ErrGoalNotFound) {
			return domain.Goal{}, fmt.Errorf("%w: parent goal %s does not exist", domain.ErrInvalidGoal, spec.ParentID)
		}
		if err != nil {
			return domain.Goal{}, err
		}
		parent = &p
	}
	g, err := domain.NewGoal(chart, name, spec, parent, s.ids.New("gol"), s.clock.Now())
	if err != nil {
		return domain.Goal{}, err
	}
	if err := s.repo.CreateGoal(ctx, g); err != nil {
		return domain.Goal{}, fmt.Errorf("store goal: %w", err)
	}
	return g, s.goalChanged(ctx, name, "goal.set", g)
}

// UpdateGoal edits a goal for its owner or an agent above the owner.
func (s *Society) UpdateGoal(ctx context.Context, id string, u domain.GoalUpdate) (domain.Goal, error) {
	name, g, err := s.changeableGoal(ctx, id)
	if err != nil {
		return domain.Goal{}, err
	}
	next, err := u.Apply(g, s.clock.Now())
	if err != nil {
		return domain.Goal{}, err
	}
	if err := s.repo.SaveGoal(ctx, next); err != nil {
		return domain.Goal{}, fmt.Errorf("save goal: %w", err)
	}
	return next, s.goalChanged(ctx, name, "goal.edit", next)
}

// DeleteGoal deletes a goal for its owner or an agent above the owner.
func (s *Society) DeleteGoal(ctx context.Context, id string) error {
	name, g, err := s.changeableGoal(ctx, id)
	if err != nil {
		return err
	}
	if err := s.repo.DeleteGoal(ctx, id); err != nil {
		return fmt.Errorf("delete goal: %w", err)
	}
	return s.goalChanged(ctx, name, "goal.delete", g)
}

func (s *Society) changeableGoal(ctx context.Context, id string) (string, domain.Goal, error) {
	name, err := actor(ctx)
	if err != nil {
		return "", domain.Goal{}, err
	}
	g, err := s.repo.GetGoal(ctx, id)
	if err != nil {
		return "", domain.Goal{}, err
	}
	chart, err := s.chart(ctx)
	if err != nil {
		return "", domain.Goal{}, err
	}
	if err := domain.CanChangeGoal(chart, name, g); err != nil {
		return "", domain.Goal{}, err
	}
	return name, g, nil
}

func (s *Society) goalChanged(ctx context.Context, actorName, action string, g domain.Goal) error {
	data := editedData{Agent: g.Owner, Fields: []string{"goals"}}
	return s.record(ctx, "agent:"+actorName, action, "goal:"+g.ID, map[string]string{"owner": g.Owner, "title": g.Title, "status": string(g.Status)},
		event{"agentsone.agents.edited", "/agents/" + actorName, "agent:" + g.Owner, data})
}
