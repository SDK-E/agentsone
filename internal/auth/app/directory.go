// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"fmt"

	"github.com/SDK-E/agentsone/internal/core"
)

var _ core.HumanDirectory = (*Service)(nil)

// HumanAccounts returns every human with their account number, for the
// humans' Linux users. It carries no password hash and no session.
func (s *Service) HumanAccounts(ctx context.Context) ([]core.HumanAccount, error) {
	hs, err := s.d.Humans.ListHumans(ctx)
	if err != nil {
		return nil, fmt.Errorf("list human accounts: %w", err)
	}
	out := make([]core.HumanAccount, 0, len(hs))
	for _, h := range hs {
		out = append(out, core.HumanAccount{Name: h.Name, UID: h.UID})
	}
	return out, nil
}

// HumanAccount returns the human called name, or ErrNotFound.
func (s *Service) HumanAccount(ctx context.Context, name string) (core.HumanAccount, error) {
	h, err := s.d.Humans.Human(ctx, name)
	if err != nil {
		return core.HumanAccount{}, fmt.Errorf("human account: %w", err)
	}
	return core.HumanAccount{Name: h.Name, UID: h.UID}, nil
}

// HumanByUID returns the human whose account number, and Linux uid, is
// uid, or ErrNotFound. Other errors are failed lookups.
func (s *Service) HumanByUID(ctx context.Context, uid int) (core.HumanAccount, error) {
	h, err := s.d.Humans.HumanByUID(ctx, uid)
	if err != nil {
		return core.HumanAccount{}, fmt.Errorf("human account of uid %d: %w", uid, err)
	}
	return core.HumanAccount{Name: h.Name, UID: h.UID}, nil
}
