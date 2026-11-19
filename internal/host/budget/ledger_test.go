// SPDX-License-Identifier: MIT

package budget_test

import (
	"errors"
	"testing"

	"github.com/SDK-E/agentsone/internal/host/budget"
)

const usd = budget.Micros(1_000_000)

var (
	worker = budget.Caller{Agent: "worker"}
	waker  = budget.Caller{Agent: "waker", Defence: true}
)

func ledger(t *testing.T, total budget.Micros, defenceOn bool, share int) *budget.Ledger {
	t.Helper()
	l := budget.NewLedger(budget.Snapshot{})
	if err := l.SetTotal(total); err != nil {
		t.Fatalf("SetTotal: %v", err)
	}
	if err := l.SetDefenceShare(share); err != nil {
		t.Fatalf("SetDefenceShare: %v", err)
	}
	l.SetDefence(defenceOn)
	return l
}

func TestReserveRespectsWallsAndDefenceSlice(t *testing.T) {
	tests := []struct {
		name      string
		total     budget.Micros
		defenceOn bool
		share     int
		spendBy   budget.Caller
		spend     budget.Micros
		caller    budget.Caller
		amount    budget.Micros
		want      error
	}{
		{"fits under total", 10 * usd, false, 0, worker, 0, worker, 10 * usd, nil},
		{"over total refused", 10 * usd, false, 0, worker, 0, worker, 10*usd + 1, budget.ErrBudgetExhausted},
		{"zero total refuses", 0, false, 0, worker, 0, worker, 1, budget.ErrBudgetExhausted},
		{"after spending, rest fits", 10 * usd, false, 0, worker, 4 * usd, worker, 6 * usd, nil},
		{"after spending, more refused", 10 * usd, false, 0, worker, 4 * usd, worker, 6*usd + 1, budget.ErrBudgetExhausted},
		{"non-defence cannot use slice", 10 * usd, true, 20, worker, 0, worker, 8*usd + 1, budget.ErrBudgetExhausted},
		{"non-defence uses the rest", 10 * usd, true, 20, worker, 0, worker, 8 * usd, nil},
		{"defence may use whole total", 10 * usd, true, 20, worker, 0, waker, 10 * usd, nil},
		{"defence gets slice after others drain", 10 * usd, true, 20, worker, 8 * usd, waker, 2 * usd, nil},
		{"defence still capped by total", 10 * usd, true, 20, worker, 8 * usd, waker, 2*usd + 1, budget.ErrBudgetExhausted},
		{"defence spend inside slice leaves general pool", 10 * usd, true, 20, waker, 2 * usd, worker, 8 * usd, nil},
		{"defence spend past slice eats general pool", 10 * usd, true, 20, waker, 3 * usd, worker, 7*usd + 1, budget.ErrBudgetExhausted},
		{"slice switched off frees it", 10 * usd, false, 20, worker, 0, worker, 10 * usd, nil},
		{"share 100 blocks all non-defence", 10 * usd, true, 100, worker, 0, worker, 1, budget.ErrBudgetExhausted},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			l := ledger(t, tc.total, tc.defenceOn, tc.share)
			if tc.spend > 0 {
				id, err := l.Reserve(tc.spendBy, tc.spend)
				if err != nil {
					t.Fatalf("setup Reserve: %v", err)
				}
				if err := l.Settle(id, tc.spend); err != nil {
					t.Fatalf("setup Settle: %v", err)
				}
			}
			_, err := l.Reserve(tc.caller, tc.amount)
			if !errors.Is(err, tc.want) {
				t.Fatalf("Reserve error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestReservationsHoldBudgetUntilSettled(t *testing.T) {
	l := ledger(t, 10*usd, false, 0)
	id, err := l.Reserve(worker, 6*usd)
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if _, err := l.Reserve(worker, 5*usd); !errors.Is(err, budget.ErrBudgetExhausted) {
		t.Fatalf("second Reserve error = %v, want ErrBudgetExhausted", err)
	}
	// The call cost less than estimated; the difference comes back.
	if err := l.Settle(id, 1*usd); err != nil {
		t.Fatalf("Settle: %v", err)
	}
	if _, err := l.Reserve(worker, 9*usd); err != nil {
		t.Fatalf("Reserve after settle: %v", err)
	}
	st := l.State()
	if st.Spent != 1*usd || st.Reserved != 9*usd || st.Remaining != 0 {
		t.Fatalf("state = %+v", st)
	}
}

func TestSettleIsNotCountedTwice(t *testing.T) {
	l := ledger(t, 10*usd, false, 0)
	id, err := l.Reserve(worker, 2*usd)
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if err := l.Settle(id, 2*usd); err != nil {
		t.Fatalf("Settle: %v", err)
	}
	if err := l.Settle(id, 2*usd); !errors.Is(err, budget.ErrUnknownReservation) {
		t.Fatalf("second Settle error = %v, want ErrUnknownReservation", err)
	}
	if got := l.State().Spent; got != 2*usd {
		t.Fatalf("spent = %v, want 2 USD", got)
	}
}

func TestSettleOverEstimateIsRecordedAndBlocksNextCall(t *testing.T) {
	l := ledger(t, 10*usd, false, 0)
	id, err := l.Reserve(worker, 9*usd)
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if err := l.Settle(id, 11*usd); err != nil {
		t.Fatalf("Settle: %v", err)
	}
	st := l.State()
	if st.Spent != 11*usd || st.Remaining != 0 || !st.Exhausted {
		t.Fatalf("state = %+v", st)
	}
	if _, err := l.Reserve(waker, 1); !errors.Is(err, budget.ErrBudgetExhausted) {
		t.Fatalf("Reserve error = %v, want ErrBudgetExhausted", err)
	}
}

func TestReleaseReturnsWholeReservation(t *testing.T) {
	l := ledger(t, 10*usd, false, 0)
	id, err := l.Reserve(worker, 10*usd)
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if err := l.Release(id); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if got := l.State().Remaining; got != 10*usd {
		t.Fatalf("remaining = %v, want 10 USD", got)
	}
}

func TestInvalidInputsAreRefused(t *testing.T) {
	l := ledger(t, 10*usd, false, 0)
	if _, err := l.Reserve(worker, 0); !errors.Is(err, budget.ErrInvalidAmount) {
		t.Fatalf("Reserve(0) error = %v", err)
	}
	if _, err := l.Reserve(budget.Caller{}, 1); !errors.Is(err, budget.ErrInvalidCaller) {
		t.Fatalf("Reserve(no agent) error = %v", err)
	}
	id, _ := l.Reserve(worker, 1)
	if err := l.Settle(id, -1); !errors.Is(err, budget.ErrInvalidAmount) {
		t.Fatalf("Settle(-1) error = %v", err)
	}
	if err := l.SetTotal(-1); !errors.Is(err, budget.ErrInvalidAmount) {
		t.Fatalf("SetTotal(-1) error = %v", err)
	}
	for _, pct := range []int{-1, 101} {
		if err := l.SetDefenceShare(pct); !errors.Is(err, budget.ErrInvalidShare) {
			t.Fatalf("SetDefenceShare(%d) error = %v", pct, err)
		}
	}
}

func TestAllocationsAreSoftPerAgentLimits(t *testing.T) {
	l := ledger(t, 10*usd, true, 10)
	l.SetAllocations(map[string]budget.Micros{"worker": 2 * usd, "waker": 1})

	if _, err := l.Reserve(worker, 2*usd); err != nil {
		t.Fatalf("Reserve within allocation: %v", err)
	}
	if _, err := l.Reserve(worker, 1); !errors.Is(err, budget.ErrAllocationExhausted) {
		t.Fatalf("Reserve over allocation error = %v, want ErrAllocationExhausted", err)
	}
	// Agents without an allocation draw from the shared pool.
	if _, err := l.Reserve(budget.Caller{Agent: "other"}, 3*usd); err != nil {
		t.Fatalf("Reserve without allocation: %v", err)
	}
	// Defence callers are not held to the soft split.
	if _, err := l.Reserve(waker, 2*usd); err != nil {
		t.Fatalf("defence Reserve over its allocation: %v", err)
	}
	if got := l.State().Agents["worker"]; got.Used != 2*usd || got.Allocation != 2*usd {
		t.Fatalf("worker view = %+v", got)
	}
}

func TestRemainingForCaller(t *testing.T) {
	l := ledger(t, 10*usd, true, 20)
	l.SetAllocations(map[string]budget.Micros{"worker": 3 * usd})
	if got := l.RemainingFor(worker); got != 3*usd {
		t.Fatalf("worker remaining = %v, want 3 USD (allocation)", got)
	}
	if got := l.RemainingFor(budget.Caller{Agent: "other"}); got != 8*usd {
		t.Fatalf("other remaining = %v, want 8 USD", got)
	}
	if got := l.RemainingFor(waker); got != 10*usd {
		t.Fatalf("waker remaining = %v, want 10 USD", got)
	}
}

func TestStateReportsDefenceSlice(t *testing.T) {
	l := ledger(t, 10*usd, true, 25)
	st := l.State()
	if st.Total != 10*usd || !st.DefenceOn || st.DefenceSharePct != 25 ||
		st.DefenceReserve != 2_500_000 || st.Remaining != 7_500_000 || st.DefenceRemaining != 10*usd {
		t.Fatalf("state = %+v", st)
	}
	if st.Exhausted || st.DefenceExhausted {
		t.Fatalf("fresh ledger reported exhausted: %+v", st)
	}
}

func TestSnapshotCountsOpenReservationsAsSpent(t *testing.T) {
	l := ledger(t, 10*usd, true, 20)
	l.SetAllocations(map[string]budget.Micros{"worker": 5 * usd})
	if _, err := l.Reserve(worker, 3*usd); err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	id, err := l.Reserve(waker, 1*usd)
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if err := l.Settle(id, 1*usd); err != nil {
		t.Fatalf("Settle: %v", err)
	}

	snap := l.Snapshot()
	if snap.GeneralSpent != 3*usd || snap.DefenceSpent != 1*usd || snap.AgentSpent["worker"] != 3*usd {
		t.Fatalf("snapshot = %+v", snap)
	}

	// A ledger rebuilt from the snapshot (as after a crash) never forgets money.
	again := budget.NewLedger(snap)
	st := again.State()
	if st.Spent != 4*usd || st.Reserved != 0 || st.Total != 10*usd || !st.DefenceOn || st.DefenceSharePct != 20 {
		t.Fatalf("restored state = %+v", st)
	}
	if got := again.RemainingFor(worker); got != 2*usd {
		t.Fatalf("restored worker remaining = %v, want 2 USD", got)
	}
}

func TestParseAndFormatUSD(t *testing.T) {
	tests := []struct {
		in   string
		want budget.Micros
	}{
		{"0", 0}, {"12", 12 * usd}, {"12.5", 12_500_000}, {"0.000001", 1}, {"1,000.25", 1_000_250_000}, {"$3.10", 3_100_000},
	}
	for _, tc := range tests {
		got, err := budget.ParseUSD(tc.in)
		if err != nil || got != tc.want {
			t.Fatalf("ParseUSD(%q) = %v, %v; want %v", tc.in, got, err, tc.want)
		}
	}
	for _, bad := range []string{"", "-1", "abc", "1.0000001", "1.2.3", "99999999999999"} {
		if _, err := budget.ParseUSD(bad); err == nil {
			t.Fatalf("ParseUSD(%q) accepted a bad amount", bad)
		}
	}
	if got := budget.Micros(12_500_000).String(); got != "$12.50" {
		t.Fatalf("String = %q", got)
	}
	if got := budget.Micros(1).String(); got != "$0.000001" {
		t.Fatalf("String = %q", got)
	}
	if got := budget.Micros(-2_000_000).String(); got != "-$2.00" {
		t.Fatalf("String = %q", got)
	}
}
