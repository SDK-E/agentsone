// SPDX-License-Identifier: MIT

package domain

import (
	"errors"
	"fmt"
	"time"
)

// ErrInvalidPeriod is returned for a period that is not month, week or day.
var ErrInvalidPeriod = errors.New("budget: period must be month, week or day")

// Period is how long the total lasts before spending starts again from zero. Periods follow the
// UTC calendar: a month starts on the 1st, a week on Monday, a day at midnight.
type Period string

// The periods.
const (
	PeriodMonth Period = "month"
	PeriodWeek  Period = "week"
	PeriodDay   Period = "day"
)

// ParsePeriod reads a period. Empty means a month.
func ParsePeriod(s string) (Period, error) {
	switch p := Period(s); p {
	case "":
		return PeriodMonth, nil
	case PeriodMonth, PeriodWeek, PeriodDay:
		return p, nil
	default:
		return "", fmt.Errorf("%w: got %q", ErrInvalidPeriod, s)
	}
}

// Bounds returns the period that holds t, as [start, end) in UTC.
func (p Period) Bounds(t time.Time) (start, end time.Time) {
	t = t.UTC()
	day := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
	switch p {
	case PeriodDay:
		return day, day.AddDate(0, 0, 1)
	case PeriodWeek:
		start = day.AddDate(0, 0, -((int(t.Weekday()) + 6) % 7))
		return start, start.AddDate(0, 0, 7)
	default:
		start = time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
		return start, start.AddDate(0, 1, 0)
	}
}

// SetPeriod changes the period kind. The next Roll moves to the period of the new kind that holds
// that moment, keeping what was spent so far: changing the period never hands out money again.
func (l *Ledger) SetPeriod(p Period) {
	if p == l.period {
		return
	}
	l.period = p
	l.periodStart = time.Time{}
}

// Roll moves the ledger to the period that holds now and reports whether the period changed.
//
// When no period is set yet (a new ledger, spending stored before periods existed, or a changed
// period kind) the ledger takes the current period and keeps what it counted. When now is past
// the current period, settled spending starts again from zero; calls still open stay counted,
// since they are settled in the new period. A clock that went back changes nothing.
func (l *Ledger) Roll(now time.Time) bool {
	if l.period == "" {
		l.period = PeriodMonth
	}
	start, _ := l.period.Bounds(now)
	if l.periodStart.IsZero() {
		l.periodStart = start
		return true
	}
	if !start.After(l.periodStart) {
		return false
	}
	l.periodStart = start
	l.generalUsed, l.defenceUsed = 0, 0
	l.agentUsed = map[string]Micros{}
	for _, r := range l.reservations {
		l.add(r.caller, r.amount)
	}
	return true
}
