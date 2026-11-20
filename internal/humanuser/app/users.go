// SPDX-License-Identifier: MIT

// Package app makes sure every human has their Linux user: at each daemon
// start for all humans (a rescue resets /etc), and on demand before a
// program runs as a human (the editor, the desktop).
package app

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/humanuser/domain"
)

// Accounts makes one Linux user, idempotently (adapters/accounts).
type Accounts interface {
	Ensure(ctx context.Context, u domain.User) error
}

// Users makes humans' Linux users from the human directory.
type Users struct {
	humans   core.HumanDirectory
	accounts Accounts
	// mu keeps two callers in this daemon from running useradd for the same
	// user at once; the adapter also copes with a race it did not see.
	mu sync.Mutex
}

// New returns Users reading humans and making users through accounts.
func New(humans core.HumanDirectory, accounts Accounts) *Users {
	return &Users{humans: humans, accounts: accounts}
}

// Ensure makes the Linux user of the human called name and returns it. An
// unknown human is ErrNotFound.
func (u *Users) Ensure(ctx context.Context, name string) (domain.User, error) {
	h, err := u.humans.HumanAccount(ctx, name)
	if err != nil {
		return domain.User{}, fmt.Errorf("linux user of %s: %w", name, err)
	}
	return u.ensure(ctx, h)
}

func (u *Users) ensure(ctx context.Context, h core.HumanAccount) (domain.User, error) {
	user, err := domain.UserFor(h.Name, h.UID)
	if err != nil {
		return domain.User{}, err
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if err := u.accounts.Ensure(ctx, user); err != nil {
		return domain.User{}, fmt.Errorf("linux user of %s: %w", h.Name, err)
	}
	return user, nil
}

// Reconcile makes every human's Linux user and returns how many it made.
// One human that fails does not stop the others; their errors are joined.
func (u *Users) Reconcile(ctx context.Context) (int, error) {
	all, err := u.humans.HumanAccounts(ctx)
	if err != nil {
		return 0, fmt.Errorf("list humans: %w", err)
	}
	made := 0
	var errs []error
	for _, h := range all {
		if _, err := u.ensure(ctx, h); err != nil {
			errs = append(errs, err)
			continue
		}
		made++
	}
	return made, errors.Join(errs...)
}
