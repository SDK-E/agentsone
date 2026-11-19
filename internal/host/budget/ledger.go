// SPDX-License-Identifier: MIT

// Package budget is the host's money ledger for model calls.
//
// Rules:
//
//   - The total is a HARD wall. It is held on the host, outside the VM, and no
//     call starts that would take spending (settled plus reserved) past it.
//   - The defence slice is a share of the total kept for defence callers
//     (wakers and security work). Non-defence callers cannot spend it. Defence
//     callers may spend the whole total. The slice can be switched off.
//   - Per-agent allocations, pushed from the daemon, are SOFT: the caller's
//     identity comes from a token root inside the VM can forge. Defence callers
//     are not held to them.
//   - Usage is accounted in chunks: a call reserves an estimate first and
//     settles the actual cost afterwards.
//
// The Ledger is pure and not safe for concurrent use; the application layer
// serialises access.
package budget

import (
	"errors"
	"fmt"
	"maps"
)

// Errors returned by the ledger. Callers match them with errors.Is.
var (
	ErrBudgetExhausted     = errors.New("budget: exhausted")
	ErrAllocationExhausted = errors.New("budget: agent allocation exhausted")
	ErrUnknownReservation  = errors.New("budget: unknown reservation")
	ErrInvalidAmount       = errors.New("budget: invalid amount")
	ErrInvalidShare        = errors.New("budget: defence share must be 0 to 100")
	ErrInvalidCaller       = errors.New("budget: caller has no agent name")
)

// Caller is who spends. Defence callers may use the defence slice.
type Caller struct {
	Agent   string
	Defence bool
}

// ReservationID names one open reservation.
type ReservationID uint64

type reservation struct {
	caller Caller
	amount Micros
}

// Snapshot is the durable state of a ledger. Open reservations are counted as
// spent, so a ledger rebuilt after a crash never forgets money.
type Snapshot struct {
	Total           Micros
	DefenceOn       bool
	DefenceSharePct int
	// GeneralSpent is what non-defence callers spent.
	GeneralSpent Micros
	// DefenceSpent is what defence callers spent.
	DefenceSpent Micros
	AgentSpent   map[string]Micros
	Allocations  map[string]Micros
}

// AgentView is one agent's line in State.
type AgentView struct {
	// Used is settled plus reserved spending.
	Used Micros
	// Allocation is the soft limit, zero when none is set.
	Allocation Micros
}

// State is a read-only view of the ledger.
type State struct {
	Total           Micros
	Spent           Micros
	Reserved        Micros
	DefenceOn       bool
	DefenceSharePct int
	// DefenceReserve is the part of the total kept for defence callers.
	DefenceReserve Micros
	// Remaining is what a non-defence caller without an allocation may still reserve.
	Remaining Micros
	// DefenceRemaining is what a defence caller may still reserve.
	DefenceRemaining Micros
	Exhausted        bool
	DefenceExhausted bool
	Agents           map[string]AgentView
}

// Ledger holds the budget and its spending.
type Ledger struct {
	total     Micros
	defenceOn bool
	sharePct  int

	// generalUsed and defenceUsed are settled plus reserved, by caller class.
	generalUsed Micros
	defenceUsed Micros
	reserved    Micros

	agentUsed   map[string]Micros
	allocations map[string]Micros

	next         ReservationID
	reservations map[ReservationID]reservation
}

// NewLedger rebuilds a ledger from a snapshot. A zero Snapshot gives an empty
// ledger with a total of zero, so every call is refused until a human sets one.
func NewLedger(s Snapshot) *Ledger {
	l := &Ledger{
		total:        s.Total,
		defenceOn:    s.DefenceOn,
		sharePct:     s.DefenceSharePct,
		generalUsed:  s.GeneralSpent,
		defenceUsed:  s.DefenceSpent,
		agentUsed:    maps.Clone(s.AgentSpent),
		allocations:  maps.Clone(s.Allocations),
		reservations: map[ReservationID]reservation{},
	}
	if l.agentUsed == nil {
		l.agentUsed = map[string]Micros{}
	}
	if l.allocations == nil {
		l.allocations = map[string]Micros{}
	}
	return l
}

// SetTotal sets the hard total. It may be set below what is already spent;
// then everything is refused.
func (l *Ledger) SetTotal(total Micros) error {
	if total < 0 {
		return fmt.Errorf("%w: total %v is negative", ErrInvalidAmount, total)
	}
	l.total = total
	return nil
}

// SetDefence switches the defence slice on or off.
func (l *Ledger) SetDefence(on bool) { l.defenceOn = on }

// SetDefenceShare sets the defence slice as a whole percentage of the total.
func (l *Ledger) SetDefenceShare(pct int) error {
	if pct < 0 || pct > 100 {
		return fmt.Errorf("%w: got %d", ErrInvalidShare, pct)
	}
	l.sharePct = pct
	return nil
}

// SetAllocations replaces every soft per-agent allocation. Agents left out
// have none and draw on the shared pool.
func (l *Ledger) SetAllocations(a map[string]Micros) {
	l.allocations = map[string]Micros{}
	for agent, amount := range a {
		if amount > 0 {
			l.allocations[agent] = amount
		}
	}
}

func (l *Ledger) defenceReserve() Micros {
	if !l.defenceOn {
		return 0
	}
	return l.total / 100 * Micros(l.sharePct)
}

// generalAvailable is what non-defence callers may still reserve. Defence
// spending inside the slice does not reduce it; spending past the slice does.
func (l *Ledger) generalAvailable() Micros {
	return clamp(l.total - l.generalUsed - max(l.defenceUsed, l.defenceReserve()))
}

func (l *Ledger) defenceAvailable() Micros {
	return clamp(l.total - l.generalUsed - l.defenceUsed)
}

func clamp(m Micros) Micros { return max(m, 0) }

// RemainingFor is what this caller may still reserve.
func (l *Ledger) RemainingFor(c Caller) Micros {
	if c.Defence {
		return l.defenceAvailable()
	}
	left := l.generalAvailable()
	if alloc, ok := l.allocations[c.Agent]; ok {
		left = min(left, clamp(alloc-l.agentUsed[c.Agent]))
	}
	return left
}

// Reserve holds amount for a call about to start. The hard wall is checked
// first, then the caller's soft allocation.
func (l *Ledger) Reserve(c Caller, amount Micros) (ReservationID, error) {
	if c.Agent == "" {
		return 0, ErrInvalidCaller
	}
	if amount <= 0 {
		return 0, fmt.Errorf("%w: reservation %v must be positive", ErrInvalidAmount, amount)
	}
	if c.Defence {
		if amount > l.defenceAvailable() {
			return 0, fmt.Errorf("%w: %v needed, %v left for defence", ErrBudgetExhausted, amount, l.defenceAvailable())
		}
	} else {
		if amount > l.generalAvailable() {
			return 0, fmt.Errorf("%w: %v needed, %v left", ErrBudgetExhausted, amount, l.generalAvailable())
		}
		if alloc, ok := l.allocations[c.Agent]; ok && l.agentUsed[c.Agent]+amount > alloc {
			return 0, fmt.Errorf("%w: agent %q has %v of %v left", ErrAllocationExhausted,
				c.Agent, clamp(alloc-l.agentUsed[c.Agent]), alloc)
		}
	}
	l.next++
	id := l.next
	l.reservations[id] = reservation{caller: c, amount: amount}
	l.add(c, amount)
	l.reserved += amount
	return id, nil
}

// Settle closes a reservation with the call's actual cost. The actual cost may
// be above the estimate; it is recorded in full. Settling the same
// reservation twice returns ErrUnknownReservation and changes nothing.
func (l *Ledger) Settle(id ReservationID, actual Micros) error {
	if actual < 0 {
		return fmt.Errorf("%w: actual cost %v is negative", ErrInvalidAmount, actual)
	}
	r, ok := l.reservations[id]
	if !ok {
		return fmt.Errorf("%w: %d", ErrUnknownReservation, id)
	}
	delete(l.reservations, id)
	l.reserved -= r.amount
	l.add(r.caller, actual-r.amount)
	return nil
}

// Release closes a reservation for a call that cost nothing.
func (l *Ledger) Release(id ReservationID) error { return l.Settle(id, 0) }

func (l *Ledger) add(c Caller, delta Micros) {
	if c.Defence {
		l.defenceUsed += delta
	} else {
		l.generalUsed += delta
	}
	l.agentUsed[c.Agent] += delta
}

// State returns a view of the ledger.
func (l *Ledger) State() State {
	agents := map[string]AgentView{}
	for a, used := range l.agentUsed {
		agents[a] = AgentView{Used: used}
	}
	for a, alloc := range l.allocations {
		v := agents[a]
		v.Allocation = alloc
		agents[a] = v
	}
	spent := l.generalUsed + l.defenceUsed - l.reserved
	return State{
		Total:            l.total,
		Spent:            spent,
		Reserved:         l.reserved,
		DefenceOn:        l.defenceOn,
		DefenceSharePct:  l.sharePct,
		DefenceReserve:   l.defenceReserve(),
		Remaining:        l.generalAvailable(),
		DefenceRemaining: l.defenceAvailable(),
		Exhausted:        l.generalAvailable() == 0,
		DefenceExhausted: l.defenceAvailable() == 0,
		Agents:           agents,
	}
}

// Snapshot returns the durable state, counting open reservations as spent.
func (l *Ledger) Snapshot() Snapshot {
	return Snapshot{
		Total:           l.total,
		DefenceOn:       l.defenceOn,
		DefenceSharePct: l.sharePct,
		GeneralSpent:    l.generalUsed,
		DefenceSpent:    l.defenceUsed,
		AgentSpent:      maps.Clone(l.agentUsed),
		Allocations:     maps.Clone(l.allocations),
	}
}
