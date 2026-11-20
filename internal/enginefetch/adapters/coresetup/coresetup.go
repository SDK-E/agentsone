// SPDX-License-Identifier: MIT

// Package coresetup connects the engine fetcher to the daemon's
// core.EngineSetup port, owned by internal/auth: the licence gate and the
// fetch status the setup page shows.
package coresetup

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/enginefetch/app"
	"github.com/SDK-E/agentsone/internal/enginefetch/domain"
)

// Setup is app.LicenceGate and app.StatusSink on core.Ports.EngineSetup.
// The port is read on each call, because modules fill Ports after their
// constructors ran.
type Setup struct {
	ports      *core.Ports
	log        *slog.Logger
	warnOnce   sync.Once // the port cannot answer
	notYetOnce sync.Once // no human accepted the licence yet
}

var (
	_ app.LicenceGate = (*Setup)(nil)
	_ app.StatusSink  = (*Setup)(nil)
)

// New returns the adapter on p, logging to log.
func New(p *core.Ports, log *slog.Logger) *Setup { return &Setup{ports: p, log: log} }

// Accepted reports whether a human accepted the current engine licence.
// A licence nobody accepted yet is the normal state of a new machine, noted
// once at info. A port that is not provided or cannot read the licence
// counts as not accepted too, but it is a fault, since no human can accept
// the licence then: it is warned about once, with its cause.
func (s *Setup) Accepted(ctx context.Context) (bool, error) {
	p := s.ports.EngineSetup
	if p == nil {
		s.unavailable(core.ErrUnavailable)
		return false, nil
	}
	ok, err := p.LicenceAccepted(ctx)
	if errors.Is(err, core.ErrUnavailable) {
		s.unavailable(err)
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("engine setup: %w", err)
	}
	if !ok {
		s.notYetOnce.Do(func() {
			s.log.Info("Claude engine not set up yet: a human must accept its licence")
		})
	}
	return ok, nil
}

func (s *Setup) unavailable(err error) {
	s.warnOnce.Do(func() {
		s.log.Warn("engine setup is not available, so the engine licence counts as not accepted", "err", err)
	})
}

// SaveStatus stores st through the port, field by field.
func (s *Setup) SaveStatus(ctx context.Context, st domain.Status) error {
	p := s.ports.EngineSetup
	if p == nil {
		return fmt.Errorf("engine setup: %w", core.ErrUnavailable)
	}
	err := p.SaveFetchStatus(ctx, core.EngineFetchStatus{
		State: string(st.State), BytesDone: st.BytesDone, BytesTotal: st.BytesTotal,
		Version: st.Version, Error: st.Error, UpdatedAt: st.UpdatedAt,
	})
	if err != nil {
		return fmt.Errorf("engine setup: %w", err)
	}
	return nil
}
