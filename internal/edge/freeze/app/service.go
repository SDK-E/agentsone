// SPDX-License-Identifier: MIT

// Package app runs the freeze-all switch: it keeps the egress cut, the model-call gate and the
// saved state in step, and records each change as edge proof.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/SDK-E/agentsone/internal/edge/freeze/domain"
)

// Egress is the internet filter's freeze port: it cuts and restores all egress of the machine.
type Egress interface {
	Freeze(ctx context.Context) error
	Release(ctx context.Context) error
	Frozen(ctx context.Context) (bool, error)
}

// Store keeps the switch across edge restarts.
type Store interface {
	// Load returns the saved state; the zero state when nothing was saved yet.
	Load(ctx context.Context) (domain.State, error)
	Save(ctx context.Context, s domain.State) error
}

// Proof is one edge proof record about the switch.
type Proof struct {
	Action  string
	Outcome string
	Details map[string]any
}

// Proofs records what the edge did, in the edge's own audit chain.
type Proofs interface {
	RecordProof(ctx context.Context, p Proof) error
}

// Clock tells the time.
type Clock interface {
	Now() time.Time
}

// Proof actions and outcomes.
const (
	ActionFreeze  = "freeze.on"
	ActionRelease = "freeze.off"
	ActionAdopt   = "freeze.adopt"

	OutcomeSuccess = "success"
	OutcomeFailure = "failure"
)

// Deps are the ports of the service. Proofs may be nil: then no proof is recorded.
type Deps struct {
	Egress Egress
	Store  Store
	Proofs Proofs
	Clock  Clock
	Log    *slog.Logger
}

// Service is the freeze-all switch. It is safe for concurrent use. It implements the edge's
// Freezer port, so the parts that start after it see the whole switch.
type Service struct {
	d Deps
	// mu serialises changes; state is read under it too, which is a cheap in-memory read.
	mu    sync.Mutex
	state domain.State
	// saved is false while the state in memory is not on disk yet, so the next request retries.
	saved bool
}

// ErrMissingPort is returned by Open when a required port is nil.
var ErrMissingPort = errors.New("freeze: missing port")

// Open loads the saved switch and makes egress match it. It fails closed: a state it cannot read,
// or a filter that cannot say whether it is frozen, freezes. A filter that is already frozen while
// the saved state is open is adopted, so the two never disagree. Open fails only when a freeze it
// must hold cannot cut egress; the edge must then not start.
func Open(ctx context.Context, d Deps) (*Service, error) {
	if d.Egress == nil || d.Store == nil || d.Clock == nil {
		return nil, fmt.Errorf("%w: egress, store and clock are required", ErrMissingPort)
	}
	if d.Log == nil {
		d.Log = slog.New(slog.DiscardHandler)
	}
	s := &Service{d: d, saved: true}
	state, loadErr := d.Store.Load(ctx)
	egressFrozen, egressErr := d.Egress.Frozen(ctx)
	adopt := false
	switch {
	case loadErr != nil:
		d.Log.Error("freeze: saved state unreadable, freezing", "err", loadErr)
		state, s.saved = domain.State{Frozen: true, Since: d.Clock.Now()}, false
	case egressErr != nil:
		d.Log.Error("freeze: internet filter cannot say whether it is frozen, freezing", "err", egressErr)
		if !state.Frozen {
			state, s.saved = domain.State{Frozen: true, Since: d.Clock.Now()}, false
		}
	case egressFrozen && !state.Frozen:
		state, s.saved, adopt = domain.State{Frozen: true, Since: d.Clock.Now()}, false, true
	}
	s.state = state
	if !state.Frozen {
		return s, nil
	}
	if err := d.Egress.Freeze(ctx); err != nil {
		return nil, fmt.Errorf("freeze: hold the saved freeze: %w", err)
	}
	saveErr := s.save(ctx)
	if adopt {
		s.prove(ctx, ActionAdopt, saveErr)
	}
	return s, nil
}

// Freeze turns the switch on. Model calls stop at once, before anything is saved; then the state
// is saved and egress is cut. Each step runs even when an earlier one failed, and the switch stays
// on whatever fails: the error says what did.
func (s *Service) Freeze(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next, change := s.state.Freeze(s.d.Clock.Now())
	if change == domain.Unchanged {
		// Assert again: a cut or a save that failed earlier is retried.
		return errors.Join(s.save(ctx), s.d.Egress.Freeze(ctx))
	}
	s.state, s.saved = next, false
	err := errors.Join(s.save(ctx), s.d.Egress.Freeze(ctx))
	s.prove(ctx, ActionFreeze, err)
	return err
}

// Release turns the switch off. The release is saved first, then egress is restored, and only
// then do model calls resume. When a step fails the switch stays on.
func (s *Service) Release(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next, change := s.state.Release(s.d.Clock.Now())
	if change == domain.Unchanged {
		return nil
	}
	if err := s.d.Store.Save(ctx, next); err != nil {
		err = fmt.Errorf("freeze: save release: %w", err)
		s.prove(ctx, ActionRelease, err)
		return err
	}
	if err := s.d.Egress.Release(ctx); err != nil {
		err = fmt.Errorf("freeze: restore egress: %w", err)
		s.saved = false
		err = errors.Join(err, s.save(ctx))
		s.prove(ctx, ActionRelease, err)
		return err
	}
	s.state, s.saved = next, true
	s.prove(ctx, ActionRelease, nil)
	return nil
}

// Frozen reports the switch. The gateway asks it on every model call.
func (s *Service) Frozen(context.Context) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state.Frozen, nil
}

// Status returns the switch and since when.
func (s *Service) Status() domain.State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

// save writes the state in memory unless it is already on disk. Callers hold mu.
func (s *Service) save(ctx context.Context) error {
	if s.saved {
		return nil
	}
	if err := s.d.Store.Save(ctx, s.state); err != nil {
		return fmt.Errorf("freeze: save: %w", err)
	}
	s.saved = true
	return nil
}

// prove records a change as edge proof. A proof that cannot be written is logged: the switch
// never waits on the audit log.
func (s *Service) prove(ctx context.Context, action string, failure error) {
	p := Proof{Action: action, Outcome: OutcomeSuccess, Details: map[string]any{"frozen": s.state.Frozen}}
	if failure != nil {
		p.Outcome = OutcomeFailure
		s.d.Log.Error("freeze: change failed", "action", action, "err", failure)
	}
	if s.d.Proofs == nil {
		return
	}
	if err := s.d.Proofs.RecordProof(ctx, p); err != nil {
		s.d.Log.Error("freeze: record proof", "action", action, "err", err)
	}
}
