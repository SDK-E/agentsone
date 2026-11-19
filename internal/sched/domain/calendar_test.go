// SPDX-License-Identifier: MIT

package domain_test

import (
	"errors"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/sched/domain"
)

func at(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

func TestCalendarNext_FindsTheNextFireAfterATime(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, expr, after, want string
	}{
		{"daily time later today", "*-*-* 09:00:00", "2026-11-19T08:00:00Z", "2026-11-19T09:00:00Z"},
		{"daily time already past", "*-*-* 09:00:00", "2026-11-19T09:00:00Z", "2026-11-20T09:00:00Z"},
		{"weekday and time", "Fri *-*-* 16:00:00", "2026-11-19T18:40:00Z", "2026-11-20T16:00:00Z"},
		{"weekday range", "Mon..Fri *-*-* 09:00", "2026-11-20T10:00:00Z", "2026-11-23T09:00:00Z"},
		{"weekday list", "Mon,Wed 12:00", "2026-11-19T00:00:00Z", "2026-11-23T12:00:00Z"},
		{"hourly shortcut", "hourly", "2026-11-19T18:40:00Z", "2026-11-19T19:00:00Z"},
		{"minutely shortcut", "minutely", "2026-11-19T18:40:30Z", "2026-11-19T18:41:00Z"},
		{"daily shortcut", "daily", "2026-11-19T18:40:00Z", "2026-11-20T00:00:00Z"},
		{"weekly shortcut", "weekly", "2026-11-19T18:40:00Z", "2026-11-23T00:00:00Z"},
		{"monthly shortcut", "monthly", "2026-11-19T18:40:00Z", "2026-12-01T00:00:00Z"},
		{"yearly shortcut", "yearly", "2026-11-19T18:40:00Z", "2027-01-01T00:00:00Z"},
		{"quarterly shortcut", "quarterly", "2026-11-19T18:40:00Z", "2027-01-01T00:00:00Z"},
		{"semiannually shortcut", "semiannually", "2026-11-19T18:40:00Z", "2027-01-01T00:00:00Z"},
		{"minute step", "*:0/15", "2026-11-19T18:40:00Z", "2026-11-19T18:45:00Z"},
		{"every step from star", "*-*-* *:*/20:00", "2026-11-19T18:40:00Z", "2026-11-19T19:00:00Z"},
		{"minute list", "*:10,50", "2026-11-19T18:20:00Z", "2026-11-19T18:50:00Z"},
		{"day of month", "*-*-15 06:00", "2026-11-19T00:00:00Z", "2026-12-15T06:00:00Z"},
		{"day 31 skips short months", "*-*-31 00:00", "2026-11-19T00:00:00Z", "2026-12-31T00:00:00Z"},
		{"month range", "*-01..03-01 00:00", "2026-11-19T00:00:00Z", "2027-01-01T00:00:00Z"},
		{"fixed date", "2026-12-25 08:30", "2026-11-19T00:00:00Z", "2026-12-25T08:30:00Z"},
		{"month and day only", "12-25 08:30", "2026-11-19T00:00:00Z", "2026-12-25T08:30:00Z"},
		{"leap day", "*-02-29 00:00", "2026-11-19T00:00:00Z", "2028-02-29T00:00:00Z"},
		{"seconds", "*:*:30", "2026-11-19T18:40:30Z", "2026-11-19T18:41:30Z"},
		{"weekday only means midnight", "Sat", "2026-11-19T00:00:00Z", "2026-11-21T00:00:00Z"},
		{"time zone of after is ignored", "*-*-* 09:00:00", "2026-11-19T12:00:00+02:00", "2026-11-20T09:00:00Z"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cal, err := domain.ParseCalendar(tt.expr)
			if err != nil {
				t.Fatalf("ParseCalendar(%q) error = %v", tt.expr, err)
			}
			got, ok := cal.Next(at(tt.after))
			if !ok || !got.Equal(at(tt.want)) {
				t.Fatalf("Next(%s) = %s, %v, want %s", tt.after, got.Format(time.RFC3339), ok, tt.want)
			}
		})
	}
}

func TestCalendarNext_ReportsAnExpressionThatNeverFires(t *testing.T) {
	t.Parallel()
	cal, err := domain.ParseCalendar("2020-01-01 00:00")
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := cal.Next(at("2026-11-19T00:00:00Z")); ok {
		t.Fatalf("Next() = %s, want no fire", got)
	}
}

func TestParseCalendar_RejectsBadExpressions(t *testing.T) {
	t.Parallel()
	for _, expr := range []string{
		"", "   ", "nonsense", "25:00", "*:60", "*-13-*", "*-*-32", "Funday 10:00", "10:00 10:00",
		"*:5..2", "*:*/0", "Fri..Mon 10:00", "*-*-* 10:00:00:00", "1-2-3-4", "*:a",
	} {
		t.Run(expr, func(t *testing.T) {
			t.Parallel()
			if _, err := domain.ParseCalendar(expr); !errors.Is(err, domain.ErrInvalidCalendar) {
				t.Fatalf("ParseCalendar(%q) error = %v, want %v", expr, err, domain.ErrInvalidCalendar)
			}
		})
	}
}

func TestCalendarString_KeepsTheOriginalText(t *testing.T) {
	t.Parallel()
	cal, err := domain.ParseCalendar("  Fri *-*-* 16:00:00 ")
	if err != nil {
		t.Fatal(err)
	}
	if cal.String() != "Fri *-*-* 16:00:00" {
		t.Fatalf("String() = %q", cal.String())
	}
}
