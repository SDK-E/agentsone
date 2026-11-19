// SPDX-License-Identifier: MIT

package domain

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// ErrNotDue is returned when a schedule is fired before its time.
var ErrNotDue = errors.New("schedule is not due")

const (
	maxScheduleName    = 80
	maxScheduleMessage = 2000
	// maxEventName is the name length the event catalog allows.
	maxEventName = 64
)

// Schedule is an agent's own timer. When it comes due the agent wakes with
// the message.
type Schedule struct {
	ID        string
	Agent     string
	Name      string
	Calendar  Calendar
	Message   string
	Enabled   bool
	NextRun   *time.Time // nil when disabled or the calendar has ended
	LastRun   *time.Time
	CreatedAt time.Time
	CreatedBy Actor
}

// NewScheduleParams are the fields of a new schedule.
type NewScheduleParams struct {
	ID        string
	Agent     string
	Name      string
	Calendar  string
	Message   string
	Enabled   bool
	CreatedBy Actor
	Now       time.Time
}

// NewSchedule builds a schedule and works out its first run.
func NewSchedule(p NewScheduleParams) (Schedule, error) {
	s := Schedule{
		ID: p.ID, Agent: p.Agent, Name: strings.TrimSpace(p.Name), Message: p.Message,
		Enabled: p.Enabled, CreatedAt: p.Now.UTC(), CreatedBy: p.CreatedBy,
	}
	if p.Agent == "" {
		return Schedule{}, fmt.Errorf("%w: a schedule needs an agent", ErrInvalid)
	}
	if err := s.validateWording(); err != nil {
		return Schedule{}, err
	}
	cal, err := parseNeverEnding(p.Calendar, p.Now)
	if err != nil {
		return Schedule{}, err
	}
	s.Calendar = cal
	s.plan(p.Now)
	return s, nil
}

func (s Schedule) validateWording() error {
	switch {
	case s.Name == "" || utf8.RuneCountInString(s.Name) > maxScheduleName:
		return fmt.Errorf("%w: name needs 1 to %d characters", ErrInvalid, maxScheduleName)
	case utf8.RuneCountInString(s.Message) > maxScheduleMessage:
		return fmt.Errorf("%w: message is over %d characters", ErrInvalid, maxScheduleMessage)
	}
	return nil
}

// parseNeverEnding reads expr and refuses one that would never fire.
func parseNeverEnding(expr string, now time.Time) (Calendar, error) {
	cal, err := ParseCalendar(expr)
	if err != nil {
		return Calendar{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	if _, ok := cal.Next(now); !ok {
		return Calendar{}, fmt.Errorf("%w: calendar %q never fires", ErrInvalid, cal)
	}
	return cal, nil
}

// plan sets NextRun from now.
func (s *Schedule) plan(now time.Time) {
	s.NextRun = nil
	if !s.Enabled {
		return
	}
	if next, ok := s.Calendar.Next(now); ok {
		s.NextRun = &next
	}
}

// Due reports whether the schedule should fire at now.
func (s Schedule) Due(now time.Time) bool {
	return s.Enabled && s.NextRun != nil && !s.NextRun.After(now)
}

// Fire records a run at now and returns the time that was due. The next run
// is the first one after now, so runs missed during downtime fire once, not
// once each.
func (s *Schedule) Fire(now time.Time) (time.Time, error) {
	if !s.Due(now) {
		return time.Time{}, ErrNotDue
	}
	due := *s.NextRun
	last := now.UTC()
	s.LastRun = &last
	s.plan(now)
	return due, nil
}

// ScheduleEdit is a change to a schedule. Nil fields stay.
type ScheduleEdit struct {
	Name     *string
	Calendar *string
	Message  *string
	Enabled  *bool
}

// Edit applies e and works out the next run again when the calendar or the
// enabled flag changed. A refused edit leaves the schedule untouched.
func (s *Schedule) Edit(e ScheduleEdit, now time.Time) error {
	next := *s
	if e.Name != nil {
		next.Name = strings.TrimSpace(*e.Name)
	}
	if e.Message != nil {
		next.Message = *e.Message
	}
	if err := next.validateWording(); err != nil {
		return err
	}
	replan := false
	if e.Calendar != nil {
		cal, err := parseNeverEnding(*e.Calendar, now)
		if err != nil {
			return err
		}
		next.Calendar, replan = cal, true
	}
	if e.Enabled != nil {
		replan = replan || *e.Enabled != next.Enabled
		next.Enabled = *e.Enabled
	}
	if replan {
		next.plan(now)
	}
	*s = next
	return nil
}

// EventName is the name as the event catalog allows it, at most 64 characters.
func (s Schedule) EventName() string {
	runes := []rune(s.Name)
	if len(runes) > maxEventName {
		runes = runes[:maxEventName]
	}
	return string(runes)
}

// CanManageSchedule says whether actor may create, change or delete a
// schedule of an agent: that agent itself, an agent above it, or a human.
// owner is the actor's standing to the schedule's agent.
func CanManageSchedule(actor Actor, owner Standing) error {
	if actor.Privileged() || owner.allows() {
		return nil
	}
	return fmt.Errorf("%w: %s may manage only its own schedules and those of agents below it", ErrNotPermitted, actor)
}
