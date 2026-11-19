// SPDX-License-Identifier: MIT

package domain_test

import (
	"errors"
	"testing"
	"time"

	budget "github.com/SDK-E/agentsone/internal/edge/budget/domain"
)

func at(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

func TestParsePeriod(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]budget.Period{"month": budget.PeriodMonth, "week": budget.PeriodWeek, "day": budget.PeriodDay, "": budget.PeriodMonth} {
		got, err := budget.ParsePeriod(in)
		if err != nil || got != want {
			t.Errorf("ParsePeriod(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"year", "Month", "monthly"} {
		if _, err := budget.ParsePeriod(in); !errors.Is(err, budget.ErrInvalidPeriod) {
			t.Errorf("ParsePeriod(%q) error = %v, want ErrInvalidPeriod", in, err)
		}
	}
}

func TestPeriodBoundsAreCalendarUTC(t *testing.T) {
	t.Parallel()
	tests := []struct {
		p          budget.Period
		now        string
		start, end string
	}{
		{budget.PeriodMonth, "2026-11-19T15:04:05+01:00", "2026-11-01T00:00:00Z", "2026-12-01T00:00:00Z"},
		{budget.PeriodMonth, "2026-12-31T23:59:59Z", "2026-12-01T00:00:00Z", "2027-01-01T00:00:00Z"},
		{budget.PeriodWeek, "2026-11-19T10:00:00Z", "2026-11-16T00:00:00Z", "2026-11-23T00:00:00Z"}, // Thursday; weeks start Monday
		{budget.PeriodWeek, "2026-11-22T23:00:00Z", "2026-11-16T00:00:00Z", "2026-11-23T00:00:00Z"}, // Sunday
		{budget.PeriodWeek, "2026-11-23T00:00:00Z", "2026-11-23T00:00:00Z", "2026-11-30T00:00:00Z"}, // Monday
		{budget.PeriodDay, "2026-11-19T00:30:00+02:00", "2026-11-18T00:00:00Z", "2026-11-19T00:00:00Z"},
	}
	for _, tc := range tests {
		start, end := tc.p.Bounds(at(tc.now))
		if !start.Equal(at(tc.start)) || !end.Equal(at(tc.end)) {
			t.Errorf("%s at %s = [%s, %s), want [%s, %s)", tc.p, tc.now, start, end, tc.start, tc.end)
		}
	}
}

func TestLedgerStartsTheFirstPeriodKeepingWhatWasCounted(t *testing.T) {
	t.Parallel()
	l := budget.NewLedger(budget.Snapshot{Total: 10 * usd, GeneralSpent: 4 * usd}) // stored before periods existed

	if !l.Roll(at("2026-11-19T10:00:00Z")) {
		t.Fatal("Roll() on a ledger with no period = false, want a new period")
	}
	st := l.State()
	if st.Period != budget.PeriodMonth || !st.PeriodStart.Equal(at("2026-11-01T00:00:00Z")) || !st.PeriodEnd.Equal(at("2026-12-01T00:00:00Z")) {
		t.Errorf("state period = %s [%s, %s)", st.Period, st.PeriodStart, st.PeriodEnd)
	}
	if st.Spent != 4*usd {
		t.Errorf("spent = %v, want the 4 USD counted before", st.Spent)
	}
}

func TestLedgerIgnoresAClockThatWentBack(t *testing.T) {
	t.Parallel()
	l := ledger(t, 10*usd, false, 0)
	l.Roll(at("2026-11-19T10:00:00Z"))

	if l.Roll(at("2026-10-30T10:00:00Z")) {
		t.Fatal("Roll() to an earlier period = true")
	}
	if st := l.State(); !st.PeriodStart.Equal(at("2026-11-01T00:00:00Z")) {
		t.Errorf("period start = %s", st.PeriodStart)
	}
}

func TestLedgerKeepsSpendingInsideThePeriod(t *testing.T) {
	t.Parallel()
	l := ledger(t, 10*usd, false, 0)
	l.Roll(at("2026-11-02T00:00:00Z"))
	id, err := l.Reserve(worker, 3*usd)
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if err := l.Settle(id, 3*usd); err != nil {
		t.Fatalf("Settle: %v", err)
	}

	if l.Roll(at("2026-11-30T23:59:59Z")) {
		t.Fatal("Roll() inside the period = true")
	}
	if got := l.State().Spent; got != 3*usd {
		t.Errorf("spent = %v, want 3 USD", got)
	}
}

func TestLedgerResetsSpendingAtTheNextPeriod(t *testing.T) {
	t.Parallel()
	l := ledger(t, 10*usd, true, 20)
	l.Roll(at("2026-11-02T00:00:00Z"))
	settled, _ := l.Reserve(worker, 3*usd)
	_ = l.Settle(settled, 3*usd)
	defence, _ := l.Reserve(waker, 1*usd)
	_ = l.Settle(defence, 1*usd)
	open, err := l.Reserve(worker, 2*usd)
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}

	if !l.Roll(at("2026-12-01T00:00:00Z")) {
		t.Fatal("Roll() at the next period = false")
	}

	st := l.State()
	if st.Spent != 0 || st.Reserved != 2*usd || st.Agents["worker"].Used != 2*usd || st.Agents["waker"].Used != 0 {
		t.Errorf("after the roll: spent %v reserved %v worker %v waker %v; want only the open call",
			st.Spent, st.Reserved, st.Agents["worker"].Used, st.Agents["waker"].Used)
	}
	if !st.PeriodStart.Equal(at("2026-12-01T00:00:00Z")) {
		t.Errorf("period start = %s", st.PeriodStart)
	}
	if err := l.Settle(open, 2*usd); err != nil {
		t.Fatalf("Settle after the roll: %v", err)
	}
	if got := l.State().Spent; got != 2*usd {
		t.Errorf("spent after settling the open call = %v, want 2 USD", got)
	}
}

func TestLedgerSetPeriodMovesToTheNewKindWithoutHandingOutMoney(t *testing.T) {
	t.Parallel()
	l := ledger(t, 10*usd, false, 0)
	l.Roll(at("2026-11-19T10:00:00Z"))
	id, _ := l.Reserve(worker, 6*usd)
	_ = l.Settle(id, 6*usd)

	l.SetPeriod(budget.PeriodDay)

	if !l.Roll(at("2026-11-19T10:00:01Z")) {
		t.Fatal("Roll() after a period change = false, want the day period to start")
	}
	st := l.State()
	if !st.PeriodStart.Equal(at("2026-11-19T00:00:00Z")) || !st.PeriodEnd.Equal(at("2026-11-20T00:00:00Z")) {
		t.Errorf("period = [%s, %s)", st.PeriodStart, st.PeriodEnd)
	}
	if st.Spent != 6*usd {
		t.Errorf("spent = %v after the change, want 6 USD kept", st.Spent)
	}
	if !l.Roll(at("2026-11-20T00:00:00Z")) || l.State().Spent != 0 {
		t.Error("the next day did not start from zero")
	}
}

func TestLedgerSnapshotKeepsThePeriod(t *testing.T) {
	t.Parallel()
	l := ledger(t, 10*usd, false, 0)
	l.SetPeriod(budget.PeriodWeek)
	l.Roll(at("2026-11-19T10:00:00Z"))

	again := budget.NewLedger(l.Snapshot())

	if again.Roll(at("2026-11-20T10:00:00Z")) {
		t.Error("a rebuilt ledger started a new period inside the stored one")
	}
	if st := again.State(); st.Period != budget.PeriodWeek || !st.PeriodStart.Equal(at("2026-11-16T00:00:00Z")) {
		t.Errorf("rebuilt period = %s from %s", st.Period, st.PeriodStart)
	}
}
