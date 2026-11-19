// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"sync"
	"time"

	"github.com/SDK-E/agentsone/internal/machine/domain"
)

// BeginReapply starts the recipe reapply, bounded by timeout, and returns at
// once, so the daemon opens its API while packages install. Until it ends,
// the machine-recipe service shows "activating". then runs with the report.
func (m *Machine) BeginReapply(ctx context.Context, timeout time.Duration, then func(domain.Report)) {
	m.recipeMu.Lock()
	m.report = domain.Report{State: domain.RecipeRunning, Started: m.d.Clock.Now()}
	m.recipeMu.Unlock()
	go func() {
		rctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		then(m.Reapply(rctx))
	}()
}

// StartupGate holds agent wakes until the machine is ready after a start.
type StartupGate struct {
	done chan struct{}
	once sync.Once
}

// NewStartupGate returns a closed gate.
func NewStartupGate() *StartupGate { return &StartupGate{done: make(chan struct{})} }

// Open lets every waiter through, now and later. A failed recipe opens it
// too: agents wake anyway and the failure shows on the machine-recipe
// service.
func (g *StartupGate) Open() { g.once.Do(func() { close(g.done) }) }

// Wait blocks until the gate opens or ctx ends.
func (g *StartupGate) Wait(ctx context.Context) error {
	select {
	case <-g.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
