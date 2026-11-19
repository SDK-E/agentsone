// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/SDK-E/agentsone/internal/core"
)

// Freeze sets a freeze lever. Every running runner the scope covers is put
// to sleep (its turn cancelled, its session compacted) and stopped, and
// wakes for it are refused until Release.
func (s *Supervisor) Freeze(ctx context.Context, scope core.FreezeScope) error {
	s.mu.Lock()
	if !slices.Contains(s.freezes, scope) {
		s.freezes = append(s.freezes, scope)
	}
	var running []string
	for name, r := range s.runners {
		if r.conn != nil || r.session.Ready() {
			running = append(running, name)
		}
	}
	s.mu.Unlock()
	slices.Sort(running)
	s.audit(ctx, ActionFreeze, string(scope.Kind)+":"+scope.Target, nil)
	var errs []error
	for _, name := range running {
		covered, err := s.covers(ctx, scope, name)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if covered {
			if err := s.Sleep(ctx, name, true); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

// Release lifts a freeze lever. Agents stay asleep until something wakes them.
func (s *Supervisor) Release(ctx context.Context, scope core.FreezeScope) error {
	s.mu.Lock()
	s.freezes = slices.DeleteFunc(s.freezes, func(f core.FreezeScope) bool { return f == scope })
	s.mu.Unlock()
	s.audit(ctx, ActionRelease, string(scope.Kind)+":"+scope.Target, nil)
	return nil
}

// State returns the active freeze levers.
func (s *Supervisor) State(context.Context) ([]core.FreezeScope, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.freezes), nil
}

// Frozen reports whether any lever covers agent name: freeze all, its team
// (the top of its chart branch) or the agent itself.
func (s *Supervisor) Frozen(ctx context.Context, name string) (bool, error) {
	s.mu.Lock()
	freezes := slices.Clone(s.freezes)
	s.mu.Unlock()
	for _, f := range freezes {
		covered, err := s.covers(ctx, f, name)
		if err != nil {
			return false, err
		}
		if covered {
			return true, nil
		}
	}
	return false, nil
}

func (s *Supervisor) covers(ctx context.Context, f core.FreezeScope, name string) (bool, error) {
	switch f.Kind {
	case core.FreezeAll:
		return true, nil
	case core.FreezeAgent:
		return f.Target == name, nil
	case core.FreezeTeam:
		team, err := s.d.Ports.Agents.TeamRoot(ctx, name)
		if err != nil {
			return false, fmt.Errorf("freeze check for %s: %w", name, err)
		}
		return team == f.Target, nil
	default:
		return false, nil
	}
}
