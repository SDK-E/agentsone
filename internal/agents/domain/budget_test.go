// SPDX-License-Identifier: MIT

package domain_test

import (
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/SDK-E/agentsone/internal/agents/agentstest"
	"github.com/SDK-E/agentsone/internal/agents/domain"
	"github.com/SDK-E/agentsone/internal/core"
)

func micros(n int64) *int64 { return &n }

func pool(total int64) domain.Pool {
	return domain.Pool{Currency: "USD", TotalMicros: total, Known: true}
}

func split(p domain.Pool, alloc map[string]int64, spent map[string]int64) domain.Split {
	return domain.NewSplit(agentstest.Chart(), p, alloc, spent)
}

func TestSplit_HoldingFollowsTheChart(t *testing.T) {
	t.Parallel()
	s := split(pool(1000), map[string]int64{"lead": 600, "dev": 200}, nil)
	tests := map[string]int64{
		"founder": 1000, // the whole total
		"lead":    600,  // explicit
		"dev":     200,  // explicit
		"intern":  200,  // shares dev's remainder
		"ops":     400,  // shares founder's remainder after lead's 600
		"watcher": 400,
	}
	for name, want := range tests {
		if got := s.Node(name).HoldingMicros; got != want {
			t.Errorf("holding(%s) = %d, want %d", name, got, want)
		}
	}
}

func TestSplit_Allocate(t *testing.T) {
	t.Parallel()
	base := map[string]int64{"lead": 600}
	tests := []struct {
		name          string
		actor, target string
		amount        *int64
		wantErr       error
	}{
		{"child, inside the holding", "founder", "ops", micros(400), nil},
		{"child, one over the holding", "founder", "ops", micros(401), domain.ErrOverAllocated},
		{"grandchild, out of its manager's holding", "founder", "dev", micros(600), nil},
		{"grandchild, one over its manager's holding", "founder", "dev", micros(601), domain.ErrOverAllocated},
		{"back to sharing the remainder", "founder", "lead", nil, nil},
		{"self", "lead", "lead", micros(1), domain.ErrNotBelowActor},
		{"peer", "lead", "ops", micros(1), domain.ErrNotBelowActor},
		{"ancestor", "dev", "lead", micros(1), domain.ErrNotBelowActor},
		{"negative amount", "founder", "ops", micros(-1), domain.ErrInvalidBudget},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := split(pool(1000), base, nil)
			_, err := s.Allocate(tt.actor, tt.target, tt.amount)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Allocate() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestSplit_AllocateRefusesShrinkingBelowTheReportsShares(t *testing.T) {
	t.Parallel()
	s := split(pool(1000), map[string]int64{"lead": 600, "dev": 300}, nil)

	_, err := s.Allocate("founder", "lead", micros(299))

	if !errors.Is(err, domain.ErrOverAllocated) {
		t.Fatalf("error = %v, want %v", err, domain.ErrOverAllocated)
	}
}

func TestSplit_AllocateRefusesSqueezingASharingSiblingsReports(t *testing.T) {
	t.Parallel()
	// ops shares founder's remainder (400) and gives watcher 400.
	s := split(pool(1000), map[string]int64{"lead": 600, "watcher": 400}, nil)

	_, err := s.Allocate("founder", "lead", micros(601))

	if !errors.Is(err, domain.ErrOverAllocated) {
		t.Fatalf("error = %v, want %v", err, domain.ErrOverAllocated)
	}
}

func TestSplit_SpendingCountsAgainstEveryShareAbove(t *testing.T) {
	t.Parallel()
	s := split(pool(1000), map[string]int64{"lead": 600, "dev": 200}, map[string]int64{"intern": 50, "dev": 100})

	got := []int64{s.Node("intern").RemainingMicros, s.Node("dev").RemainingMicros, s.Node("lead").RemainingMicros, s.Node("founder").RemainingMicros}

	// intern shares dev's 200; dev's branch spent 150.
	if diff := cmp.Diff([]int64{50, 50, 450, 850}, got); diff != "" {
		t.Fatalf("remaining intern, dev, lead, founder (-want +got):\n%s", diff)
	}
}

func TestSplit_RemainingNeverExceedsWhatTheHostLeaves(t *testing.T) {
	t.Parallel()
	p := pool(1000)
	p.SpentMicros = 1000

	s := split(p, map[string]int64{"lead": 600}, nil)

	if got := s.Node("lead").RemainingMicros; got != 0 {
		t.Fatalf("remaining(lead) = %d, want 0 once the host total is spent", got)
	}
}

func TestSplit_State(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		spent int64
		want  domain.BudgetState
	}{
		{"plenty left", 0, domain.BudgetOK},
		{"exactly a tenth left", 900, domain.BudgetOK},
		{"under a tenth left", 901, domain.BudgetLow},
		{"nothing left", 1000, domain.BudgetExhausted},
		{"overspent", 1200, domain.BudgetExhausted},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := split(pool(1000), map[string]int64{"lead": 1000}, map[string]int64{"lead": tt.spent})
			if got := s.Node("lead").State; got != tt.want {
				t.Fatalf("state = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestSplit_CanWake(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		agent   string
		spent   int64
		defence domain.Defence
		wantErr error
	}{
		{"budget above zero", "dev", 999, domain.Defence{}, nil},
		{"budget exactly zero", "dev", 1000, domain.Defence{}, core.ErrBudget},
		{"budget below zero", "dev", 1001, domain.Defence{}, core.ErrBudget},
		{"waker on the defence slice", "watcher", 1000, domain.Defence{Enabled: true, ReservedMicros: 10}, nil},
		{"waker with the defence slice off", "watcher", 1000, domain.Defence{Enabled: false, ReservedMicros: 10}, core.ErrBudget},
		{"waker with the defence slice spent", "watcher", 1000, domain.Defence{Enabled: true, ReservedMicros: 10, SpentMicros: 10}, core.ErrBudget},
		{"custom agent with the defence slice on", "dev", 1000, domain.Defence{Enabled: true, ReservedMicros: 10}, core.ErrBudget},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p := pool(1000)
			p.Defence = tt.defence
			s := split(p, nil, map[string]int64{tt.agent: tt.spent})
			if err := s.CanWake(tt.agent); !errors.Is(err, tt.wantErr) {
				t.Fatalf("CanWake() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestSplit_CanWakeWhileTheHostHasNotPushedABudget(t *testing.T) {
	t.Parallel()
	s := domain.NewSplit(agentstest.Chart(), domain.Pool{}, nil, nil)

	if err := s.CanWake("dev"); err != nil {
		t.Fatalf("CanWake() error = %v, want nil before the host pushes a budget", err)
	}
}

func TestSplit_NodesListParentsFirst(t *testing.T) {
	t.Parallel()
	var names []string
	for _, n := range split(pool(1000), nil, nil).Nodes() {
		names = append(names, n.Agent)
	}
	if diff := cmp.Diff([]string{"founder", "lead", "dev", "intern", "ops", "watcher"}, names); diff != "" {
		t.Fatalf("node order (-want +got):\n%s", diff)
	}
}

func TestPool_Validate(t *testing.T) {
	t.Parallel()
	good := domain.Pool{Currency: "USD", TotalMicros: 10, CheckIntervalSeconds: 60}
	bad := []domain.Pool{
		{Currency: "usd", TotalMicros: 10, CheckIntervalSeconds: 60},
		{Currency: "USD", TotalMicros: -1, CheckIntervalSeconds: 60},
		{Currency: "USD", TotalMicros: 10, SpentMicros: -1, CheckIntervalSeconds: 60},
		{Currency: "USD", TotalMicros: 10, CheckIntervalSeconds: 0},
	}
	if err := good.Validate(); err != nil {
		t.Fatalf("valid pool: %v", err)
	}
	for _, p := range bad {
		if err := p.Validate(); !errors.Is(err, domain.ErrInvalidBudget) {
			t.Errorf("Validate(%+v) = %v, want %v", p, err, domain.ErrInvalidBudget)
		}
	}
}
