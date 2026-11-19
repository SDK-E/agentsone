// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"fmt"

	"github.com/SDK-E/agentsone/internal/auth/domain"
	"github.com/SDK-E/agentsone/internal/core"
)

// NewHuman is an account to create.
type NewHuman struct {
	Name        string
	DisplayName string
	Password    string
}

// HumanChange is a partial update; nil fields stay as they are.
type HumanChange struct {
	DisplayName *string
	Password    *string
}

// requireHuman is the soft wall inside the machine: human accounts are a
// human power. Agents, plugins and system callers are refused.
func requireHuman(by core.Caller) error {
	if by.Kind != core.CallerHuman || by.Name == "" {
		return fmt.Errorf("only a signed-in human may manage humans: %w", core.ErrForbidden)
	}
	return nil
}

// ListHumans returns every human account.
func (s *Service) ListHumans(ctx context.Context) ([]domain.Human, error) {
	hs, err := s.d.Humans.ListHumans(ctx)
	if err != nil {
		return nil, fmt.Errorf("list humans: %w", err)
	}
	return hs, nil
}

// GetHuman returns one human account.
func (s *Service) GetHuman(ctx context.Context, name string) (domain.Human, error) {
	h, err := s.d.Humans.Human(ctx, name)
	if err != nil {
		return domain.Human{}, fmt.Errorf("get human: %w", err)
	}
	return h, nil
}

// CreateHuman adds a human account: a Linux user in the humans group.
func (s *Service) CreateHuman(ctx context.Context, by core.Caller, n NewHuman) (domain.Human, error) {
	if err := requireHuman(by); err != nil {
		return domain.Human{}, err
	}
	h, err := s.addHuman(ctx, n)
	if err != nil {
		return domain.Human{}, err
	}
	s.audit(ctx, by, "human.create", "human:"+h.Name)
	return h, nil
}

func (s *Service) addHuman(ctx context.Context, n NewHuman) (domain.Human, error) {
	if err := domain.CheckHumanName(n.Name); err != nil {
		return domain.Human{}, err
	}
	if _, err := s.d.Humans.Human(ctx, n.Name); err == nil {
		return domain.Human{}, fmt.Errorf("human %s exists: %w", n.Name, core.ErrConflict)
	}
	uid, err := s.d.Accounts.CreateUser(ctx, n.Name, n.Password)
	if err != nil {
		return domain.Human{}, fmt.Errorf("create user %s: %w", n.Name, err)
	}
	h := domain.Human{Name: n.Name, DisplayName: n.DisplayName, UID: uid, CreatedAt: s.d.Clock.Now()}
	if err := s.d.Humans.AddHuman(ctx, h); err != nil {
		return domain.Human{}, fmt.Errorf("store human %s: %w", n.Name, err)
	}
	return h, nil
}

// UpdateHuman changes a human's display name or password.
func (s *Service) UpdateHuman(ctx context.Context, by core.Caller, name string, c HumanChange) (domain.Human, error) {
	if err := requireHuman(by); err != nil {
		return domain.Human{}, err
	}
	h, err := s.d.Humans.Human(ctx, name)
	if err != nil {
		return domain.Human{}, fmt.Errorf("update human: %w", err)
	}
	if c.Password != nil {
		if err := s.d.Accounts.SetPassword(ctx, name, *c.Password); err != nil {
			return domain.Human{}, fmt.Errorf("set password of %s: %w", name, err)
		}
		// A new password ends every old sign-in.
		if err := s.d.Sessions.DeleteHumanSessions(ctx, name); err != nil {
			return domain.Human{}, fmt.Errorf("end sessions of %s: %w", name, err)
		}
	}
	if c.DisplayName != nil {
		h.DisplayName = *c.DisplayName
		if err := s.d.Humans.UpdateHuman(ctx, h); err != nil {
			return domain.Human{}, fmt.Errorf("update human %s: %w", name, err)
		}
	}
	s.audit(ctx, by, "human.update", "human:"+name)
	return h, nil
}

// DeleteHuman removes a human account. The last human cannot be removed,
// or nobody could sign in again.
func (s *Service) DeleteHuman(ctx context.Context, by core.Caller, name string) error {
	if err := requireHuman(by); err != nil {
		return err
	}
	if _, err := s.d.Humans.Human(ctx, name); err != nil {
		return fmt.Errorf("delete human: %w", err)
	}
	all, err := s.d.Humans.ListHumans(ctx)
	if err != nil {
		return fmt.Errorf("delete human: %w", err)
	}
	if len(all) == 1 {
		return fmt.Errorf("%s is the last human: %w", name, core.ErrConflict)
	}
	if err := s.d.Accounts.DeleteUser(ctx, name); err != nil {
		return fmt.Errorf("delete user %s: %w", name, err)
	}
	if err := s.d.Sessions.DeleteHumanSessions(ctx, name); err != nil {
		return fmt.Errorf("end sessions of %s: %w", name, err)
	}
	if err := s.d.Humans.DeleteHuman(ctx, name); err != nil {
		return fmt.Errorf("delete human %s: %w", name, err)
	}
	s.audit(ctx, by, "human.delete", "human:"+name)
	return nil
}
