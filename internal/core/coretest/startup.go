// SPDX-License-Identifier: MIT

package coretest

import (
	"context"
	"sync"

	"github.com/SDK-E/agentsone/internal/core"
)

// Startup is a core.StartupGate that starts open. Hold closes it and Open
// opens it again, so tests can check that wakes wait for the machine.
type Startup struct {
	mu   sync.Mutex
	open chan struct{}
}

var _ core.StartupGate = (*Startup)(nil)

// NewStartup returns an open gate.
func NewStartup() *Startup {
	s := &Startup{open: make(chan struct{})}
	close(s.open)
	return s
}

// Hold closes the gate until Open.
func (s *Startup) Hold() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.open = make(chan struct{})
}

// Open lets waiters through.
func (s *Startup) Open() {
	s.mu.Lock()
	defer s.mu.Unlock()
	select {
	case <-s.open:
	default:
		close(s.open)
	}
}

// Wait blocks until the gate is open or ctx ends.
func (s *Startup) Wait(ctx context.Context) error {
	s.mu.Lock()
	open := s.open
	s.mu.Unlock()
	select {
	case <-open:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
