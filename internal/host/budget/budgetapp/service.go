// SPDX-License-Identifier: MIT

// Package budgetapp holds the budget use cases: reserve and settle a call's
// cost, set the total and the defence slice, take allocations from the
// daemon, and flush the ledger to the store at an interval.
//
// Human settings (total, defence switch and share) are saved at once. Spending
// is kept in memory and flushed on every tick of the flush interval and when
// the service stops. Each flush also reloads the human settings, so a change
// made by another process (the `agentsone budget` command) takes effect within
// one interval.
package budgetapp

import (
	"context"
	"fmt"
	"maps"
	"sync"
	"time"

	"github.com/SDK-E/agentsone/internal/host/budget"
)

// DefaultFlushInterval is how often spending is written to the store.
const DefaultFlushInterval = 5 * time.Second

// Settings are the human-owned budget settings.
type Settings struct {
	Total           budget.Micros
	DefenceOn       bool
	DefenceSharePct int
}

// Spend is the spending recorded so far, open reservations included.
type Spend struct {
	General budget.Micros
	Defence budget.Micros
	Agents  map[string]budget.Micros
}

// Store keeps settings and spending across restarts.
type Store interface {
	LoadSettings(ctx context.Context) (Settings, error)
	SaveSettings(ctx context.Context, s Settings) error
	LoadSpend(ctx context.Context) (Spend, error)
	SaveSpend(ctx context.Context, s Spend) error
}

// Service is safe for concurrent use.
type Service struct {
	store Store

	mu     sync.Mutex
	ledger *budget.Ledger
	dirty  bool
}

// Open loads settings and spending from the store.
func Open(ctx context.Context, store Store) (*Service, error) {
	set, err := store.LoadSettings(ctx)
	if err != nil {
		return nil, fmt.Errorf("budget: load settings: %w", err)
	}
	sp, err := store.LoadSpend(ctx)
	if err != nil {
		return nil, fmt.Errorf("budget: load spending: %w", err)
	}
	l := budget.NewLedger(budget.Snapshot{
		Total:           set.Total,
		DefenceOn:       set.DefenceOn,
		DefenceSharePct: set.DefenceSharePct,
		GeneralSpent:    sp.General,
		DefenceSpent:    sp.Defence,
		AgentSpent:      sp.Agents,
	})
	return &Service{store: store, ledger: l}, nil
}

// Reserve holds an estimated cost before a call.
func (s *Service) Reserve(c budget.Caller, estimate budget.Micros) (budget.ReservationID, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, err := s.ledger.Reserve(c, estimate)
	if err == nil {
		s.dirty = true
	}
	return id, err
}

// Settle records a call's actual cost.
func (s *Service) Settle(id budget.ReservationID, actual budget.Micros) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ledger.Settle(id, actual); err != nil {
		return err
	}
	s.dirty = true
	return nil
}

// Release closes a reservation for a call that cost nothing.
func (s *Service) Release(id budget.ReservationID) error { return s.Settle(id, 0) }

// State returns the current view of the budget.
func (s *Service) State() budget.State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ledger.State()
}

// RemainingFor is what this caller may still reserve.
func (s *Service) RemainingFor(c budget.Caller) budget.Micros {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ledger.RemainingFor(c)
}

// SetAllocations replaces the soft per-agent allocations. The daemon pushes
// them; they are not stored, and the daemon pushes them again after a restart.
func (s *Service) SetAllocations(a map[string]budget.Micros) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ledger.SetAllocations(maps.Clone(a))
}

// SetTotal sets the hard total and saves it.
func (s *Service) SetTotal(ctx context.Context, total budget.Micros) error {
	return s.changeSettings(ctx, func(set *Settings) { set.Total = total })
}

// SetDefence switches the defence slice on or off and saves it.
func (s *Service) SetDefence(ctx context.Context, on bool) error {
	return s.changeSettings(ctx, func(set *Settings) { set.DefenceOn = on })
}

// SetDefenceShare sets the defence slice percentage and saves it.
func (s *Service) SetDefenceShare(ctx context.Context, pct int) error {
	return s.changeSettings(ctx, func(set *Settings) { set.DefenceSharePct = pct })
}

func (s *Service) changeSettings(ctx context.Context, change func(*Settings)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.ledger.State()
	set := Settings{Total: st.Total, DefenceOn: st.DefenceOn, DefenceSharePct: st.DefenceSharePct}
	change(&set)
	// Validate on a scratch ledger so a refused value changes nothing.
	if err := ApplySettings(budget.NewLedger(budget.Snapshot{}), set); err != nil {
		return err
	}
	if err := s.store.SaveSettings(ctx, set); err != nil {
		return fmt.Errorf("budget: save settings: %w", err)
	}
	return ApplySettings(s.ledger, set)
}

// ApplySettings puts settings on a ledger.
func ApplySettings(l *budget.Ledger, set Settings) error {
	if err := l.SetTotal(set.Total); err != nil {
		return err
	}
	if err := l.SetDefenceShare(set.DefenceSharePct); err != nil {
		return err
	}
	l.SetDefence(set.DefenceOn)
	return nil
}

// Flush reloads the human settings and writes spending when it changed.
func (s *Service) Flush(ctx context.Context) error {
	set, err := s.store.LoadSettings(ctx)
	if err != nil {
		return fmt.Errorf("budget: reload settings: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ApplySettings(s.ledger, set); err != nil {
		return fmt.Errorf("budget: stored settings: %w", err)
	}
	if !s.dirty {
		return nil
	}
	snap := s.ledger.Snapshot()
	sp := Spend{General: snap.GeneralSpent, Defence: snap.DefenceSpent, Agents: snap.AgentSpent}
	if err := s.store.SaveSpend(ctx, sp); err != nil {
		return fmt.Errorf("budget: save spending: %w", err)
	}
	s.dirty = false
	return nil
}

// Run flushes on every tick until ctx ends, then flushes once more and returns
// the result of that last flush. Errors from tick flushes go to onErr when it
// is not nil; the loop keeps going.
func (s *Service) Run(ctx context.Context, ticks <-chan time.Time, onErr func(error)) error {
	for {
		select {
		case <-ctx.Done():
			return s.Flush(context.WithoutCancel(ctx))
		case <-ticks:
			if err := s.Flush(ctx); err != nil && onErr != nil {
				onErr(err)
			}
		}
	}
}
