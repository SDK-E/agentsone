// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/events/domain"
)

// CreateTrigger stores a trigger. An agent owns the triggers it creates and
// may only point them at itself or at agents below it. For any other caller
// the owner is the agent the action targets.
func (s *Service) CreateTrigger(ctx context.Context, spec domain.TriggerSpec) (domain.Trigger, error) {
	c := callerOf(ctx)
	if err := spec.Validate(); err != nil {
		return domain.Trigger{}, err
	}
	target := spec.Action.Agent
	if _, err := s.d.Ports.Agents.GetAgent(ctx, target); err != nil {
		if errors.Is(err, core.ErrNotFound) {
			return domain.Trigger{}, fmt.Errorf("%w: agent %q does not exist", core.ErrInvalid, target)
		}
		return domain.Trigger{}, err
	}
	owner := target
	if c.Kind == core.CallerAgent && !c.IsRoot {
		owner = c.Name
		if target != c.Name && !s.isBelow(ctx, c.Name)(target) {
			return domain.Trigger{}, fmt.Errorf("%w: a trigger may only act on its owner or an agent below it", core.ErrForbidden)
		}
	}
	t := domain.NewTrigger(spec, s.d.IDs.New("trg"), owner, s.d.Clock.Now())
	if err := s.d.Triggers.Create(ctx, t); err != nil {
		return domain.Trigger{}, err
	}
	s.audit(ctx, actorName(c), "trigger.create", "trigger:"+t.ID, map[string]any{"name": t.Name, "owner": t.Owner})
	return t, nil
}

// GetTrigger returns a trigger the caller may see.
func (s *Service) GetTrigger(ctx context.Context, id string) (domain.Trigger, error) {
	t, err := s.d.Triggers.Get(ctx, id)
	if err != nil {
		return domain.Trigger{}, err
	}
	if !s.viewerOf(ctx, callerOf(ctx)).CanSeeAgent(t.Owner) {
		return domain.Trigger{}, fmt.Errorf("trigger %s: %w", id, core.ErrNotFound)
	}
	return t, nil
}

// ListTriggers returns up to limit visible triggers after afterID, and
// whether more follow. A non-empty owner keeps only that agent's triggers.
func (s *Service) ListTriggers(ctx context.Context, owner, afterID string, limit int) ([]domain.Trigger, bool, error) {
	viewer := s.viewerOf(ctx, callerOf(ctx))
	var out []domain.Trigger
	cursor := afterID
	for {
		batch, err := s.d.Triggers.List(ctx, cursor, replayPage)
		if err != nil {
			return nil, false, err
		}
		for _, t := range batch {
			cursor = t.ID
			if !viewer.CanSeeAgent(t.Owner) || (owner != "" && t.Owner != owner) {
				continue
			}
			if len(out) == limit {
				return out, true, nil
			}
			out = append(out, t)
		}
		if len(batch) < replayPage {
			return out, false, nil
		}
	}
}

// UpdateTrigger changes the fields set in p.
func (s *Service) UpdateTrigger(ctx context.Context, id string, p domain.TriggerPatch) (domain.Trigger, error) {
	c := callerOf(ctx)
	t, err := s.manageable(ctx, c, id)
	if err != nil {
		return domain.Trigger{}, err
	}
	updated, err := t.Apply(p)
	if err != nil {
		return domain.Trigger{}, err
	}
	if p.Action != nil && updated.Action.Agent != t.Action.Agent && c.Kind == core.CallerAgent && !c.IsRoot &&
		updated.Action.Agent != c.Name && !s.isBelow(ctx, c.Name)(updated.Action.Agent) {
		return domain.Trigger{}, fmt.Errorf("%w: a trigger may only act on its owner or an agent below it", core.ErrForbidden)
	}
	if err := s.d.Triggers.Update(ctx, updated); err != nil {
		return domain.Trigger{}, err
	}
	s.audit(ctx, actorName(c), "trigger.update", "trigger:"+id, map[string]any{"name": updated.Name})
	return updated, nil
}

// DeleteTrigger removes a trigger.
func (s *Service) DeleteTrigger(ctx context.Context, id string) error {
	c := callerOf(ctx)
	t, err := s.manageable(ctx, c, id)
	if err != nil {
		return err
	}
	if err := s.d.Triggers.Delete(ctx, id); err != nil {
		return err
	}
	s.limiter.Forget("trigger:" + id)
	s.audit(ctx, actorName(c), "trigger.delete", "trigger:"+id, map[string]any{"name": t.Name})
	return nil
}

// manageable returns the trigger when c may change it: its owner, a manager
// above the owner, or a privileged or human caller.
func (s *Service) manageable(ctx context.Context, c core.Caller, id string) (domain.Trigger, error) {
	t, err := s.GetTrigger(ctx, id)
	if err != nil {
		return domain.Trigger{}, err
	}
	switch {
	case c.Kind != core.CallerAgent || c.IsRoot, t.Owner == c.Name:
		return t, nil
	case s.isBelow(ctx, c.Name)(t.Owner):
		return t, nil
	}
	return domain.Trigger{}, fmt.Errorf("%w: trigger %s belongs to %s", core.ErrForbidden, id, t.Owner)
}

// fire runs every enabled trigger that matches e. A failing trigger never
// fails the publish.
func (s *Service) fire(ctx context.Context, e core.Event) {
	triggers, err := s.d.Triggers.Enabled(ctx)
	if err != nil {
		s.d.Log.WarnContext(ctx, "list triggers failed", "error", err)
		return
	}
	for _, t := range triggers {
		if t.Matches(e) {
			s.run(ctx, t, e)
		}
	}
}

func (s *Service) run(ctx context.Context, t domain.Trigger, e core.Event) {
	owner := core.Caller{Kind: core.CallerAgent, Name: t.Owner}
	if !s.viewerOf(ctx, owner).CanSee(e) {
		return
	}
	if !s.limiter.Allow("trigger:"+t.ID, t.Rate.Rate()) {
		if err := s.d.Triggers.RecordDrop(ctx, t.ID); err != nil {
			s.d.Log.WarnContext(ctx, "record trigger drop failed", "trigger", t.ID, "error", err)
		}
		return
	}
	err := s.act(core.WithCaller(ctx, systemCaller), t, e)
	if errors.Is(err, core.ErrFrozen) {
		s.d.Log.InfoContext(ctx, "trigger skipped: agent is frozen", "trigger", t.ID, "agent", t.Action.Agent)
		return
	}
	if err != nil {
		s.d.Log.WarnContext(ctx, "trigger action failed", "trigger", t.ID, "error", err)
		return
	}
	if err := s.d.Triggers.RecordFire(ctx, t.ID, s.d.Clock.Now()); err != nil {
		s.d.Log.WarnContext(ctx, "record trigger fire failed", "trigger", t.ID, "error", err)
	}
	s.audit(ctx, "system:daemon", "trigger.fire", "trigger:"+t.ID,
		map[string]any{"event": e.ID, "action": string(t.Action.Kind), "agent": t.Action.Agent})
}

func (s *Service) act(ctx context.Context, t domain.Trigger, e core.Event) error {
	a := t.Action
	if a.Kind == domain.ActionCreateTask {
		_, err := s.d.Ports.Tasks.CreateTask(ctx, core.NewTask{
			Title:        domain.Render(a.Title, e),
			Body:         fmt.Sprintf("Created by trigger %q from event %s.", t.Name, e.ID),
			Assignee:     a.Agent,
			Priority:     a.Priority,
			CreatedBy:    t.Owner,
			CauseEventID: e.ID,
		})
		return err
	}
	payload, err := json.Marshal(map[string]any{
		"trigger_id": t.ID,
		"message":    domain.Render(a.Message, e),
		"event":      e,
	})
	if err != nil {
		return fmt.Errorf("encode wake payload: %w", err)
	}
	return s.d.Ports.Waker.Wake(ctx, a.Agent, core.WakeEvent, payload)
}
