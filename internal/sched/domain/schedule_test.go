// SPDX-License-Identifier: MIT

package domain_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/sched/domain"
)

func newSchedule(t *testing.T, opts ...func(*domain.NewScheduleParams)) domain.Schedule {
	t.Helper()
	p := domain.NewScheduleParams{
		ID: "sch_1", Agent: "founder", Name: "Morning report", Calendar: "*-*-* 09:00:00",
		Message: "Write the report.", Enabled: true, CreatedBy: agent("founder"),
		Now: time.Date(2026, 11, 19, 8, 0, 0, 0, time.UTC),
	}
	for _, o := range opts {
		o(&p)
	}
	s, err := domain.NewSchedule(p)
	if err != nil {
		t.Fatalf("NewSchedule() error = %v", err)
	}
	return s
}

func TestNewSchedule_ComputesTheNextRun(t *testing.T) {
	t.Parallel()
	s := newSchedule(t)
	want := time.Date(2026, 11, 19, 9, 0, 0, 0, time.UTC)
	if s.NextRun == nil || !s.NextRun.Equal(want) || s.LastRun != nil {
		t.Fatalf("next %v last %v, want next %v and no last run", s.NextRun, s.LastRun, want)
	}
}

func TestNewSchedule_DisabledHasNoNextRun(t *testing.T) {
	t.Parallel()
	s := newSchedule(t, func(p *domain.NewScheduleParams) { p.Enabled = false })
	if s.NextRun != nil {
		t.Fatalf("NextRun = %v, want none", s.NextRun)
	}
}

func TestNewSchedule_RejectsBadFields(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		opt  func(*domain.NewScheduleParams)
	}{
		{"empty name", func(p *domain.NewScheduleParams) { p.Name = "" }},
		{"name of 81 characters", func(p *domain.NewScheduleParams) { p.Name = strings.Repeat("x", 81) }},
		{"message of 2001 characters", func(p *domain.NewScheduleParams) { p.Message = strings.Repeat("x", 2001) }},
		{"calendar that does not parse", func(p *domain.NewScheduleParams) { p.Calendar = "whenever" }},
		{"calendar that never fires", func(p *domain.NewScheduleParams) { p.Calendar = "2020-01-01 00:00" }},
		{"no agent", func(p *domain.NewScheduleParams) { p.Agent = "" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p := domain.NewScheduleParams{
				ID: "sch_1", Agent: "founder", Name: "n", Calendar: "hourly", Message: "m",
				Enabled: true, CreatedBy: agent("founder"), Now: epoch,
			}
			tt.opt(&p)
			if _, err := domain.NewSchedule(p); !errors.Is(err, domain.ErrInvalid) {
				t.Fatalf("NewSchedule() error = %v, want %v", err, domain.ErrInvalid)
			}
		})
	}
}

func TestScheduleDue(t *testing.T) {
	t.Parallel()
	s := newSchedule(t)
	nine := time.Date(2026, 11, 19, 9, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		now  time.Time
		want bool
	}{
		{"one second before", nine.Add(-time.Second), false},
		{"on the minute", nine, true},
		{"long after", nine.Add(72 * time.Hour), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := s.Due(tt.now); got != tt.want {
				t.Fatalf("Due(%v) = %v, want %v", tt.now, got, tt.want)
			}
		})
	}
}

func TestScheduleDue_FalseWhenDisabled(t *testing.T) {
	t.Parallel()
	s := newSchedule(t)
	off := false
	if err := s.Edit(domain.ScheduleEdit{Enabled: &off}, epoch); err != nil {
		t.Fatal(err)
	}
	if s.Due(time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatal("a disabled schedule was due")
	}
}

func TestScheduleFire_SkipsMissedRunsAndFiresOnce(t *testing.T) {
	t.Parallel()
	s := newSchedule(t)
	due := *s.NextRun
	now := time.Date(2026, 11, 22, 10, 30, 0, 0, time.UTC) // three days of downtime

	fired, err := s.Fire(now)

	if err != nil {
		t.Fatalf("Fire() error = %v", err)
	}
	if !fired.Equal(due) {
		t.Fatalf("Fire() due = %v, want %v", fired, due)
	}
	wantNext := time.Date(2026, 11, 23, 9, 0, 0, 0, time.UTC)
	if s.NextRun == nil || !s.NextRun.Equal(wantNext) || s.LastRun == nil || !s.LastRun.Equal(now) {
		t.Fatalf("next %v last %v, want next %v last %v", s.NextRun, s.LastRun, wantNext, now)
	}
	if s.Due(now) {
		t.Fatal("still due after firing, would fire again")
	}
}

func TestScheduleFire_RefusesWhenNotDue(t *testing.T) {
	t.Parallel()
	s := newSchedule(t)
	if _, err := s.Fire(s.NextRun.Add(-time.Second)); !errors.Is(err, domain.ErrNotDue) {
		t.Fatalf("Fire() error = %v, want %v", err, domain.ErrNotDue)
	}
}

func TestScheduleFire_ClearsNextRunWhenTheCalendarEnds(t *testing.T) {
	t.Parallel()
	s := newSchedule(t, func(p *domain.NewScheduleParams) { p.Calendar = "2026-11-19 09:00" })
	if _, err := s.Fire(*s.NextRun); err != nil {
		t.Fatal(err)
	}
	if s.NextRun != nil {
		t.Fatalf("NextRun = %v, want none after the last fire", s.NextRun)
	}
}

func TestScheduleEdit_RecomputesNextRunWhenCalendarChanges(t *testing.T) {
	t.Parallel()
	s := newSchedule(t)
	cal := "*-*-* 18:00:00"
	if err := s.Edit(domain.ScheduleEdit{Calendar: &cal}, time.Date(2026, 11, 19, 8, 30, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 11, 19, 18, 0, 0, 0, time.UTC)
	if !s.NextRun.Equal(want) || s.Calendar.String() != cal {
		t.Fatalf("next %v calendar %q, want %v and %q", s.NextRun, s.Calendar, want, cal)
	}
}

func TestScheduleEdit_EnablingComputesNextRunFromNow(t *testing.T) {
	t.Parallel()
	s := newSchedule(t, func(p *domain.NewScheduleParams) { p.Enabled = false })
	on := true
	now := time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)
	if err := s.Edit(domain.ScheduleEdit{Enabled: &on}, now); err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 11, 26, 9, 0, 0, 0, time.UTC); !s.NextRun.Equal(want) {
		t.Fatalf("NextRun = %v, want %v", s.NextRun, want)
	}
}

func TestScheduleEdit_ChangesNameAndMessageWithoutMovingNextRun(t *testing.T) {
	t.Parallel()
	s := newSchedule(t)
	before := *s.NextRun
	name, msg := "Renamed", "New text"
	if err := s.Edit(domain.ScheduleEdit{Name: &name, Message: &msg}, epoch); err != nil {
		t.Fatal(err)
	}
	if s.Name != "Renamed" || s.Message != "New text" || !s.NextRun.Equal(before) {
		t.Fatalf("schedule = %+v", s)
	}
}

func TestScheduleEdit_RejectsBadFieldsAndKeepsTheSchedule(t *testing.T) {
	t.Parallel()
	empty, bad := "", "whenever"
	for name, e := range map[string]domain.ScheduleEdit{
		"empty name": {Name: &empty}, "bad calendar": {Calendar: &bad},
	} {
		s := newSchedule(t)
		if err := s.Edit(e, epoch); !errors.Is(err, domain.ErrInvalid) {
			t.Errorf("%s: Edit() error = %v, want %v", name, err, domain.ErrInvalid)
		}
		if s.Name != "Morning report" || s.Calendar.String() != "*-*-* 09:00:00" {
			t.Errorf("%s: schedule changed to %+v", name, s)
		}
	}
}

func TestScheduleEventName_FitsTheCatalogLimit(t *testing.T) {
	t.Parallel()
	s := newSchedule(t, func(p *domain.NewScheduleParams) { p.Name = strings.Repeat("é", 80) })
	if got := len([]rune(s.EventName())); got != 64 {
		t.Fatalf("EventName() has %d characters, want 64", got)
	}
}

func TestCanManageSchedule(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		actor   domain.Actor
		owner   domain.Standing
		wantErr error
	}{
		{"owner manages its own", agent("founder"), domain.Standing{Self: true}, nil},
		{"agent above the owner manages", agent("boss"), domain.Standing{Above: true}, nil},
		{"unrelated agent is refused", agent("other"), domain.Standing{}, domain.ErrNotPermitted},
		{"human manages any", human("ada"), domain.Standing{}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if err := domain.CanManageSchedule(tt.actor, tt.owner); !errors.Is(err, tt.wantErr) {
				t.Fatalf("CanManageSchedule() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}
