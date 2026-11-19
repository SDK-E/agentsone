// SPDX-License-Identifier: MIT

package domain

import (
	"fmt"
	"maps"
	"regexp"
	"time"

	"github.com/SDK-E/agentsone/internal/core"
)

// Budget errors.
var (
	ErrInvalidBudget = fmt.Errorf("invalid budget: %w", core.ErrInvalid)
	ErrOverAllocated = fmt.Errorf("allocation exceeds what the manager holds: %w", core.ErrConflict)
)

// BudgetState says how much is left.
type BudgetState string

// Budget states.
const (
	BudgetOK        BudgetState = "ok"
	BudgetLow       BudgetState = "low"
	BudgetExhausted BudgetState = "exhausted"
)

// stateOf is exhausted at zero, low under a tenth of what is held.
func stateOf(remaining, holding int64) BudgetState {
	switch {
	case remaining <= 0:
		return BudgetExhausted
	case remaining*10 < holding:
		return BudgetLow
	}
	return BudgetOK
}

// Defence is the slice reserved for wakers and security work.
type Defence struct {
	Enabled        bool
	ReservedMicros int64
	SpentMicros    int64
}

// Pool is the human's total budget as the host gateway pushed it. The
// total is the hard wall, held on the host; inside the VM it is only read.
type Pool struct {
	Currency             string
	TotalMicros          int64
	SpentMicros          int64
	Defence              Defence
	PeriodStart          time.Time
	PeriodEnd            time.Time
	CheckIntervalSeconds int
	AsOf                 time.Time
	// Known is false until the host first pushes a budget.
	Known bool
}

var currencyPattern = regexp.MustCompile(`^[A-Z]{3}$`)

// Validate checks a pool pushed by the host.
func (p Pool) Validate() error {
	switch {
	case !currencyPattern.MatchString(p.Currency):
		return fmt.Errorf("%w: currency %q", ErrInvalidBudget, p.Currency)
	case p.TotalMicros < 0 || p.SpentMicros < 0 || p.Defence.ReservedMicros < 0 || p.Defence.SpentMicros < 0:
		return fmt.Errorf("%w: negative amount", ErrInvalidBudget)
	case p.CheckIntervalSeconds < 1:
		return fmt.Errorf("%w: check interval %d", ErrInvalidBudget, p.CheckIntervalSeconds)
	}
	return nil
}

// RemainingMicros is what the host total still allows, never below zero.
func (p Pool) RemainingMicros() int64 { return max(0, p.TotalMicros-p.SpentMicros) }

// State is the pool's state.
func (p Pool) State() BudgetState { return stateOf(p.RemainingMicros(), p.TotalMicros) }

// BudgetNode is one agent's soft share.
type BudgetNode struct {
	Agent   string
	Manager string
	// AllocatedMicros is nil when the agent shares its manager's remainder.
	AllocatedMicros *int64
	// HoldingMicros is what the agent holds: its allocation, or the
	// manager's remainder.
	HoldingMicros int64
	// SpentMicros is what the agent and every agent below it spent.
	SpentMicros     int64
	RemainingMicros int64
	State           BudgetState
}

// Split is the soft budget split down the chart. Agents allocate shares to
// agents below them, never more than they hold. It is a soft wall: the
// daemon keeps it, and a root agent can bypass it. The hard wall is the
// host gateway.
type Split struct {
	chart Chart
	pool  Pool
	alloc map[string]int64
	spent map[string]int64
}

// NewSplit builds the split from explicit allocations and recorded usage.
func NewSplit(chart Chart, pool Pool, alloc, spent map[string]int64) Split {
	return Split{chart: chart, pool: pool, alloc: maps.Clone(orEmpty(alloc)), spent: maps.Clone(orEmpty(spent))}
}

func orEmpty(m map[string]int64) map[string]int64 {
	if m == nil {
		return map[string]int64{}
	}
	return m
}

// Pool returns the host total the split is cut from.
func (s Split) Pool() Pool { return s.pool }

func (s Split) holding(name string) int64 {
	a, err := s.chart.Get(name)
	if err != nil {
		return 0
	}
	if v, ok := s.alloc[name]; ok {
		return v
	}
	if a.Manager == "" {
		return s.pool.TotalMicros
	}
	return max(0, s.holding(a.Manager)-s.allocatedBelow(a.Manager))
}

// allocatedBelow sums the explicit allocations of name's direct reports.
func (s Split) allocatedBelow(name string) int64 {
	var sum int64
	for _, child := range s.chart.Children(name) {
		sum += s.alloc[child.Name]
	}
	return sum
}

func (s Split) spentBelow(name string) int64 {
	sum := s.spent[name]
	for _, child := range s.chart.Children(name) {
		sum += s.spentBelow(child.Name)
	}
	return sum
}

func (s Split) remaining(name string) int64 {
	a, err := s.chart.Get(name)
	if err != nil {
		return 0
	}
	above := s.pool.RemainingMicros()
	if a.Manager != "" {
		above = s.remaining(a.Manager)
	}
	return max(0, min(s.holding(name)-s.spentBelow(name), above))
}

// Node returns name's share.
func (s Split) Node(name string) BudgetNode {
	a, _ := s.chart.Get(name)
	n := BudgetNode{
		Agent: name, Manager: a.Manager, HoldingMicros: s.holding(name),
		SpentMicros: s.spentBelow(name), RemainingMicros: s.remaining(name),
	}
	if v, ok := s.alloc[name]; ok {
		n.AllocatedMicros = &v
	}
	n.State = stateOf(n.RemainingMicros, n.HoldingMicros)
	return n
}

// Nodes returns every active agent's share, parents before children.
func (s Split) Nodes() []BudgetNode {
	var out []BudgetNode
	var walk func([]Node)
	walk = func(nodes []Node) {
		for _, n := range nodes {
			out = append(out, s.Node(n.Agent.Name))
			walk(n.Children)
		}
	}
	walk(s.chart.Tree())
	return out
}

// Allocate returns the split after actor sets target's allocation. Nil
// means target shares its manager's remainder. Target must be below actor.
// The new split must keep every agent's reports within what it holds.
func (s Split) Allocate(actor, target string, amount *int64) (Split, error) {
	if err := s.chart.CanManage(actor, target); err != nil {
		return Split{}, err
	}
	next := NewSplit(s.chart, s.pool, s.alloc, s.spent)
	if amount == nil {
		delete(next.alloc, target)
		return next, next.check()
	}
	if *amount < 0 {
		return Split{}, fmt.Errorf("%w: allocation %d below zero", ErrInvalidBudget, *amount)
	}
	next.alloc[target] = *amount
	return next, next.check()
}

// check holds the invariant: no agent's reports hold more than it does.
func (s Split) check() error {
	for _, a := range s.chart.Agents() {
		if !a.Active() {
			continue
		}
		if below, held := s.allocatedBelow(a.Name), s.holding(a.Name); below > held {
			return fmt.Errorf("%w: reports of %s would hold %d of its %d", ErrOverAllocated, a.Name, below, held)
		}
	}
	return nil
}

// CanWake refuses a wake when the agent's share is used up. A waker may
// still wake on the defence slice while it is on and not spent. Before the
// host pushes a budget nothing is refused here; the gateway is the wall.
func (s Split) CanWake(name string) error {
	a, err := s.chart.Get(name)
	if err != nil {
		return err
	}
	if !s.pool.Known || s.remaining(name) > 0 {
		return nil
	}
	d := s.pool.Defence
	if a.Kind == KindWaker && d.Enabled && d.ReservedMicros > d.SpentMicros {
		return nil
	}
	return fmt.Errorf("budget of %s is used up: %w", name, core.ErrBudget)
}

// Core returns name's share as the BudgetView port shows it.
func (s Split) Core(name string) core.Budget {
	a, _ := s.chart.Get(name)
	n := s.Node(name)
	return core.Budget{
		Agent: name, AllocatedMicros: n.HoldingMicros, RemainingMicros: n.RemainingMicros,
		Defence: a.Kind == KindWaker && s.pool.Defence.Enabled,
	}
}
