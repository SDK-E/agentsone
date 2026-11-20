// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/SDK-E/agentsone/internal/agents/domain"
	"github.com/SDK-E/agentsone/internal/core"
)

// Freezer is the society's core.Freezer port. A frozen agent is not woken
// and its runner sleeps until release. Team and agent freezes are soft
// walls; freezing all is also held as a hard wall on the host.
type Freezer struct{ s *Society }

var _ core.Freezer = Freezer{}

// Freezer returns the Freezer port.
func (s *Society) Freezer() Freezer { return Freezer{s: s} }

// Freeze activates scope. Only the host or the daemon pulls this lever.
func (f Freezer) Freeze(ctx context.Context, scope core.FreezeScope) error {
	return f.s.setFrozen(ctx, scope, true)
}

// Release ends scope's freeze. Agents still covered by another active
// scope stay frozen.
func (f Freezer) Release(ctx context.Context, scope core.FreezeScope) error {
	return f.s.setFrozen(ctx, scope, false)
}

// State returns the active freezes sorted by their text form.
func (f Freezer) State(ctx context.Context) ([]core.FreezeScope, error) {
	texts, err := f.s.repo.Freezes(ctx)
	if err != nil {
		return nil, fmt.Errorf("read freezes: %w", err)
	}
	out := make([]core.FreezeScope, 0, len(texts))
	for _, t := range texts {
		scope, err := core.ParseFreezeScope(t)
		if err != nil {
			return nil, fmt.Errorf("stored freeze: %w", err)
		}
		out = append(out, scope)
	}
	return out, nil
}

func (s *Society) setFrozen(ctx context.Context, scope core.FreezeScope, frozen bool) error {
	released, err := s.applyFreeze(ctx, scope, frozen)
	if err != nil {
		return err
	}
	// Released always-on runners start again, idle, as at the daemon's
	// start. This runs outside the lock, since starting a runner reads the
	// chart. A runner that cannot start does not undo the release: the
	// supervisor records it and puts the reason on the agent's status.
	for _, name := range released {
		_ = s.rt.Run(ctx, name)
	}
	return nil
}

// applyFreeze records scope's freeze or release and returns the always-on
// agents a release set free.
func (s *Society) applyFreeze(ctx context.Context, scope core.FreezeScope, frozen bool) ([]string, error) {
	c, _ := core.CallerFrom(ctx)
	if c.Kind != core.CallerHost && c.Kind != core.CallerSystem {
		return nil, fmt.Errorf("freeze is a host lever: %w", core.ErrForbidden)
	}
	if _, err := core.ParseFreezeScope(scope.String()); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	chart, err := s.chart(ctx)
	if err != nil {
		return nil, err
	}
	if scope.Kind != core.FreezeAll {
		if _, err := chart.Get(scope.Target); err != nil {
			return nil, err
		}
	}
	action := "agent.release"
	if frozen {
		action = "agent.freeze"
		err = s.rt.Freeze(ctx, scope)
	} else {
		err = s.rt.Release(ctx, scope)
	}
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", action, scope, err)
	}
	if frozen {
		err = s.repo.AddFreeze(ctx, scope.String())
	} else {
		err = s.repo.RemoveFreeze(ctx, scope.String())
	}
	if err != nil {
		return nil, fmt.Errorf("save %s %s: %w", action, scope, err)
	}
	changed, err := s.syncFrozenStates(ctx, chart)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(map[string]any{"scope": scope.String(), "agents": changed})
	if err != nil {
		return nil, fmt.Errorf("encode %s: %w", action, err)
	}
	if err := s.ports.Audit.Append(ctx, core.AuditEntry{Actor: string(c.Kind) + ":" + c.Name, Action: action, Target: "freeze:" + scope.String(), Detail: raw}); err != nil {
		return nil, err
	}
	if frozen {
		return nil, nil
	}
	// A release only uncovers agents, so every agent it changed is free now.
	var released []string
	for _, name := range changed {
		if a, err := chart.Get(name); err == nil && a.Kind == domain.KindAlwaysOn {
			released = append(released, name)
		}
	}
	return released, nil
}

// syncFrozenStates sets every active agent's state from the active freezes
// and returns the names whose state changed.
func (s *Society) syncFrozenStates(ctx context.Context, chart domain.Chart) ([]string, error) {
	texts, err := s.repo.Freezes(ctx)
	if err != nil {
		return nil, fmt.Errorf("read freezes: %w", err)
	}
	var changed []domain.Agent
	for _, a := range chart.Agents() {
		if !a.Active() {
			continue
		}
		covered := covers(chart, texts, a.Name)
		switch {
		case covered && a.State != domain.StateFrozen:
			a.State = domain.StateFrozen
		case !covered && a.State == domain.StateFrozen:
			a.State = domain.StateSleeping
		default:
			continue
		}
		changed = append(changed, a)
	}
	names := make([]string, 0, len(changed))
	for _, a := range changed {
		names = append(names, a.Name)
		s.hub.Publish(a.Name, StreamOutput, Frame{State: &StateFrame{Agent: a.Name, State: a.State, Time: s.clock.Now()}})
	}
	if len(changed) == 0 {
		return names, nil
	}
	if err := s.repo.SaveAgents(ctx, changed...); err != nil {
		return nil, fmt.Errorf("save frozen states: %w", err)
	}
	return names, nil
}

// covers reports whether one of the scopes freezes name.
func covers(chart domain.Chart, scopes []string, name string) bool {
	return slices.Contains(scopes, string(core.FreezeAll)) ||
		slices.Contains(scopes, "agent:"+name) ||
		slices.Contains(scopes, "team:"+chart.Team(name))
}
