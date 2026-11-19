// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/sched/domain"
)

// Deps are what the use cases need from the daemon. Ports is read on every
// call, so a port set late (or swapped in a test) takes effect.
type Deps struct {
	Clock core.Clock
	IDs   core.IDs
	Ports *core.Ports
	Log   *slog.Logger
}

// actorOf is who made the request, from the caller on ctx.
func actorOf(ctx context.Context) (domain.Actor, error) {
	c, ok := core.CallerFrom(ctx)
	if !ok || c.Name == "" {
		return domain.Actor{}, fmt.Errorf("%w: the request has no caller", core.ErrUnauthorized)
	}
	return domain.Actor{Kind: domain.ActorKind(c.Kind), Name: c.Name}, nil
}

func isAgent(a domain.Actor) bool { return a.Kind == domain.ActorAgent }

// standing says how actor stands to the agent target: the same agent, or an
// agent above it. Only agents have a standing in the chart. An agent that is
// gone from the chart has nobody above it.
func (d Deps) standing(ctx context.Context, actor domain.Actor, target string) (domain.Standing, error) {
	if !isAgent(actor) {
		return domain.Standing{}, nil
	}
	if actor.Name == target {
		return domain.Standing{Self: true}, nil
	}
	above, err := d.Ports.Agents.IsBelow(ctx, target, actor.Name)
	if errors.Is(err, core.ErrNotFound) {
		return domain.Standing{}, nil
	}
	if err != nil {
		return domain.Standing{}, fmt.Errorf("read the chart: %w", err)
	}
	return domain.Standing{Above: above}, nil
}

// standingToActor is standing for a principal that may or may not be an agent.
func (d Deps) standingToActor(ctx context.Context, actor, other domain.Actor) (domain.Standing, error) {
	if !isAgent(other) {
		return domain.Standing{}, nil
	}
	return d.standing(ctx, actor, other.Name)
}

// requireAgent fails with ErrNotFound when the agent does not exist.
func (d Deps) requireAgent(ctx context.Context, name string) error {
	if _, err := d.Ports.Agents.GetAgent(ctx, name); err != nil {
		return fmt.Errorf("agent %q: %w", name, err)
	}
	return nil
}

// frozen reports whether the agent is frozen: by itself, by a team it is in,
// or by freezing everything.
func (d Deps) frozen(ctx context.Context, agent string) (bool, error) {
	state, err := d.Ports.Freezer.State(ctx)
	if err != nil {
		return false, fmt.Errorf("read the freezes: %w", err)
	}
	for _, scope := range state {
		switch scope.Kind {
		case core.FreezeAll:
			return true, nil
		case core.FreezeAgent:
			if scope.Target == agent {
				return true, nil
			}
		case core.FreezeTeam:
			if scope.Target == agent {
				return true, nil
			}
			below, err := d.Ports.Agents.IsBelow(ctx, agent, scope.Target)
			if err != nil && !errors.Is(err, core.ErrNotFound) {
				return false, fmt.Errorf("read the chart: %w", err)
			}
			if below {
				return true, nil
			}
		}
	}
	return false, nil
}

// audit records one fact. The change it describes has already happened, so a
// failure is logged and does not undo it.
func (d Deps) audit(ctx context.Context, actor domain.Actor, action, target string, detail map[string]any) {
	raw, err := json.Marshal(detail)
	if err != nil {
		d.Log.ErrorContext(ctx, "encode audit detail", "action", action, "err", err)
		raw = nil
	}
	entry := core.AuditEntry{Actor: actor.String(), Action: action, Target: target, Detail: raw}
	if err := d.Ports.Audit.Append(ctx, entry); err != nil {
		d.Log.ErrorContext(ctx, "write audit entry", "action", action, "target", target, "err", err)
	}
}

// coreErr maps a domain error to the core error kind the API layer reads,
// and keeps the domain's own words.
func coreErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, domain.ErrInvalid):
		return fmt.Errorf("%w: %s", core.ErrInvalid, strings.TrimPrefix(err.Error(), domain.ErrInvalid.Error()+": "))
	case errors.Is(err, domain.ErrNotPermitted):
		return fmt.Errorf("%w: %s", core.ErrForbidden, strings.TrimPrefix(err.Error(), domain.ErrNotPermitted.Error()+": "))
	case errors.Is(err, domain.ErrClosed), errors.Is(err, domain.ErrConflict):
		return fmt.Errorf("%w: %s", core.ErrConflict, err.Error())
	case errors.Is(err, domain.ErrNotFound):
		return fmt.Errorf("%w: %s", core.ErrNotFound, err.Error())
	default:
		return err
	}
}
