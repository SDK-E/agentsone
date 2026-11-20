// SPDX-License-Identifier: MIT

package coretest

import (
	"context"
	"fmt"
	"sync"

	"github.com/SDK-E/agentsone/internal/core"
)

// Humans is a fake core.HumanDirectory. It starts empty; Add fills it.
type Humans struct {
	mu     sync.Mutex
	humans []core.HumanAccount
}

var _ core.HumanDirectory = (*Humans)(nil)

// NewHumans returns an empty directory.
func NewHumans() *Humans { return &Humans{} }

// Add puts a human in the directory.
func (h *Humans) Add(name string, uid int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.humans = append(h.humans, core.HumanAccount{Name: name, UID: uid})
}

// HumanAccounts returns every human added, in order.
func (h *Humans) HumanAccounts(context.Context) ([]core.HumanAccount, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]core.HumanAccount(nil), h.humans...), nil
}

// HumanAccount returns the human called name, or ErrNotFound.
func (h *Humans) HumanAccount(_ context.Context, name string) (core.HumanAccount, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, a := range h.humans {
		if a.Name == name {
			return a, nil
		}
	}
	return core.HumanAccount{}, fmt.Errorf("human %s: %w", name, core.ErrNotFound)
}
