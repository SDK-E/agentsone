// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/events/domain"
)

const secretBytes = 32

// SeedBuiltins stores the nine built-in emitters that are not stored yet.
// Run it at start; it is safe to run again.
func (s *Service) SeedBuiltins(ctx context.Context) error {
	for _, e := range domain.BuiltinEmitters() {
		_, err := s.d.Emitters.ByName(ctx, e.Name)
		if err == nil {
			continue
		}
		if !errors.Is(err, core.ErrNotFound) {
			return err
		}
		e.ID = s.d.IDs.New("emt")
		e.CreatedAt = s.d.Clock.Now()
		if err := s.d.Emitters.Create(ctx, e); err != nil {
			return fmt.Errorf("seed emitter %s: %w", e.Name, err)
		}
	}
	return nil
}

// Registered is a new emitter and, for a webhook, its secret. The secret is
// shown once; only the daemon keeps it.
type Registered struct {
	Emitter domain.Emitter
	Secret  string
}

// ListEmitters returns up to limit emitters after afterID.
func (s *Service) ListEmitters(ctx context.Context, afterID string, limit int) ([]domain.Emitter, error) {
	return s.d.Emitters.List(ctx, afterID, limit)
}

// GetEmitter returns one emitter.
func (s *Service) GetEmitter(ctx context.Context, id string) (domain.Emitter, error) {
	return s.d.Emitters.Get(ctx, id)
}

// RegisterEmitter creates an emitter owned by the caller. A process emitter
// runs a command as a systemd unit, so only privileged callers may register
// one.
func (s *Service) RegisterEmitter(ctx context.Context, reg domain.Registration) (Registered, error) {
	c := callerOf(ctx)
	owner, err := domain.ActorOf(c)
	if err != nil {
		return Registered{}, err
	}
	if err := reg.Validate(); err != nil {
		return Registered{}, err
	}
	if reg.Kind == domain.KindProcess && !isPrivileged(c) {
		return Registered{}, fmt.Errorf("%w: only root may register a process emitter", core.ErrForbidden)
	}
	if reg.Kind == domain.KindProcess && !s.d.Units.Available() {
		return Registered{}, fmt.Errorf("%w: process emitters are not supported on this machine", core.ErrInvalid)
	}
	e := domain.NewEmitter(reg, s.d.IDs.New("emt"), owner, s.d.Clock.Now())
	if reg.Kind == domain.KindWebhook {
		secret, err := s.newSecret()
		if err != nil {
			return Registered{}, err
		}
		e.Secret = secret
	}
	if err := s.d.Emitters.Create(ctx, e); err != nil {
		return Registered{}, err
	}
	if e.Kind == domain.KindProcess {
		if err := s.d.Units.Start(ctx, e); err != nil {
			_ = s.d.Emitters.SetEnabled(ctx, e.ID, false)
			return Registered{}, fmt.Errorf("start %s: %w", e.Unit, core.ErrUnavailable)
		}
	}
	s.audit(ctx, actorName(c), "emitter.register", "emitter:"+e.ID, map[string]any{"name": e.Name, "kind": string(e.Kind)})
	return Registered{Emitter: e, Secret: e.Secret}, nil
}

func (s *Service) newSecret() (string, error) {
	buf := make([]byte, secretBytes)
	if _, err := io.ReadFull(s.d.Entropy, buf); err != nil {
		return "", fmt.Errorf("make webhook secret: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

// EnableEmitter turns an emitter on. Only its owner or a privileged caller may.
func (s *Service) EnableEmitter(ctx context.Context, id string) (domain.Emitter, error) {
	return s.setEnabled(ctx, id, true)
}

// DisableEmitter turns an emitter off.
func (s *Service) DisableEmitter(ctx context.Context, id string) (domain.Emitter, error) {
	return s.setEnabled(ctx, id, false)
}

func (s *Service) setEnabled(ctx context.Context, id string, enabled bool) (domain.Emitter, error) {
	c := callerOf(ctx)
	e, err := s.d.Emitters.Get(ctx, id)
	if err != nil {
		return domain.Emitter{}, err
	}
	if !isPrivileged(c) && e.Owner != actorName(c) {
		return domain.Emitter{}, fmt.Errorf("%w: only the owner may change emitter %s", core.ErrForbidden, e.Name)
	}
	if e.Enabled != enabled {
		if err := s.d.Emitters.SetEnabled(ctx, id, enabled); err != nil {
			return domain.Emitter{}, err
		}
		e.Enabled = enabled
		if err := s.switchUnit(ctx, e); err != nil {
			return domain.Emitter{}, err
		}
	}
	action := "emitter.disable"
	if enabled {
		action = "emitter.enable"
	}
	s.audit(ctx, actorName(c), action, "emitter:"+e.ID, map[string]any{"name": e.Name})
	return e, nil
}

func (s *Service) switchUnit(ctx context.Context, e domain.Emitter) error {
	if e.Kind != domain.KindProcess {
		return nil
	}
	var err error
	if e.Enabled {
		err = s.d.Units.Start(ctx, e)
	} else {
		err = s.d.Units.Stop(ctx, e)
	}
	if err != nil {
		return fmt.Errorf("switch %s: %w", e.Unit, core.ErrUnavailable)
	}
	return nil
}
