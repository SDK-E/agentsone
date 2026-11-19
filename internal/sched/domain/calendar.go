// SPDX-License-Identifier: MIT

package domain

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

// ErrInvalidCalendar is returned for a calendar expression this package cannot read.
var ErrInvalidCalendar = errors.New("invalid calendar expression")

// searchYears is how far ahead Next looks. It covers a leap day that also
// needs a given weekday.
const searchYears = 30

var shortcuts = map[string]string{
	"minutely":     "*-*-* *:*:00",
	"hourly":       "*-*-* *:00:00",
	"daily":        "*-*-* 00:00:00",
	"weekly":       "Mon *-*-* 00:00:00",
	"monthly":      "*-*-01 00:00:00",
	"yearly":       "*-01-01 00:00:00",
	"annually":     "*-01-01 00:00:00",
	"quarterly":    "*-01,04,07,10-01 00:00:00",
	"semiannually": "*-01,07-01 00:00:00",
}

var weekdays = []string{"mon", "tue", "wed", "thu", "fri", "sat", "sun"}

// Calendar is a systemd OnCalendar expression in UTC: an optional weekday
// list, a date (year-month-day) and a time (hour:minute:second), plus the
// shortcuts minutely, hourly, daily, weekly, monthly, quarterly,
// semiannually and yearly. Fields take "*", numbers, lists, ranges
// ("a..b") and steps ("a/n", "*/n").
type Calendar struct {
	text                                   string
	year, month, day, hour, minute, second field
	weekday                                [7]bool // Monday is 0
	weekdayAny                             bool
}

// ParseCalendar reads an OnCalendar expression.
func ParseCalendar(expr string) (Calendar, error) {
	text := strings.Join(strings.Fields(expr), " ")
	if text == "" {
		return Calendar{}, fmt.Errorf("%w: empty", ErrInvalidCalendar)
	}
	spec := text
	if full, ok := shortcuts[strings.ToLower(text)]; ok {
		spec = full
	}
	cal, err := parseSpec(spec)
	if err != nil {
		return Calendar{}, fmt.Errorf("%w: %q: %w", ErrInvalidCalendar, text, err)
	}
	cal.text = text
	return cal, nil
}

// String is the expression as written, with spaces tidied.
func (c Calendar) String() string { return c.text }

func parseSpec(spec string) (Calendar, error) {
	var datePart, timePart, dowPart string
	for _, tok := range strings.Fields(spec) {
		slot := &dowPart
		switch {
		case strings.Contains(tok, ":"):
			slot = &timePart
		case !isLetter(tok[0]):
			slot = &datePart
		}
		if *slot != "" {
			return Calendar{}, errors.New("repeated part")
		}
		*slot = tok
	}
	if datePart == "" && timePart == "" && dowPart == "" {
		return Calendar{}, errors.New("nothing to schedule")
	}
	var c Calendar
	if err := c.parseWeekdays(dowPart); err != nil {
		return Calendar{}, err
	}
	if err := c.parseDate(datePart); err != nil {
		return Calendar{}, err
	}
	if timePart == "" {
		timePart = "00:00:00"
		if datePart == "" && dowPart == "" {
			timePart = "*:*:*"
		}
	}
	if err := c.parseTime(timePart); err != nil {
		return Calendar{}, err
	}
	return c, nil
}

func isLetter(b byte) bool { return (b|0x20) >= 'a' && (b|0x20) <= 'z' }

func (c *Calendar) parseWeekdays(spec string) error {
	if spec == "" {
		c.weekdayAny = true
		return nil
	}
	for item := range strings.SplitSeq(spec, ",") {
		lo, hi, ok := strings.Cut(item, "..")
		from, err := weekdayIndex(lo)
		if err != nil {
			return err
		}
		to := from
		if ok {
			if to, err = weekdayIndex(hi); err != nil {
				return err
			}
		}
		if to < from {
			return fmt.Errorf("weekday range %q runs backwards", item)
		}
		for d := from; d <= to; d++ {
			c.weekday[d] = true
		}
	}
	return nil
}

func weekdayIndex(name string) (int, error) {
	n := strings.ToLower(name)
	if len(n) >= 3 {
		if i := slices.Index(weekdays, n[:3]); i >= 0 && strings.HasPrefix(longWeekdays[i], n) {
			return i, nil
		}
	}
	return 0, fmt.Errorf("unknown weekday %q", name)
}

var longWeekdays = []string{"monday", "tuesday", "wednesday", "thursday", "friday", "saturday", "sunday"}

func (c *Calendar) parseDate(spec string) error {
	if spec == "" {
		spec = "*-*-*"
	}
	parts := strings.Split(spec, "-")
	if len(parts) == 2 {
		parts = append([]string{"*"}, parts...)
	}
	if len(parts) != 3 {
		return fmt.Errorf("date %q needs year-month-day", spec)
	}
	var err error
	if c.year, err = parseField(parts[0], 1970, 2199); err != nil {
		return err
	}
	if c.month, err = parseField(parts[1], 1, 12); err != nil {
		return err
	}
	c.day, err = parseField(parts[2], 1, 31)
	return err
}

func (c *Calendar) parseTime(spec string) error {
	parts := strings.Split(spec, ":")
	if len(parts) == 2 {
		parts = append(parts, "0")
	}
	if len(parts) != 3 {
		return fmt.Errorf("time %q needs hour:minute[:second]", spec)
	}
	var err error
	if c.hour, err = parseField(parts[0], 0, 23); err != nil {
		return err
	}
	if c.minute, err = parseField(parts[1], 0, 59); err != nil {
		return err
	}
	c.second, err = parseField(parts[2], 0, 59)
	return err
}

// Next returns the first fire time strictly after the given time, and false
// when the expression never fires again within searchYears.
func (c Calendar) Next(after time.Time) (time.Time, bool) {
	t := after.UTC().Truncate(time.Second).Add(time.Second)
	limit := t.Year() + searchYears
	for t.Year() <= limit {
		y, mo, d := t.Date()
		switch {
		case !c.year.matches(y):
			ny, ok := c.year.next(y)
			if !ok {
				return time.Time{}, false
			}
			t = time.Date(ny, time.January, 1, 0, 0, 0, 0, time.UTC)
		case !c.month.matches(int(mo)):
			t = jump(c.month, int(mo), func(v int) time.Time { return time.Date(y, time.Month(v), 1, 0, 0, 0, 0, time.UTC) },
				time.Date(y+1, time.January, 1, 0, 0, 0, 0, time.UTC))
		case !c.day.matches(d) || !c.weekdayMatches(t):
			t = time.Date(y, mo, d+1, 0, 0, 0, 0, time.UTC)
		case !c.hour.matches(t.Hour()):
			t = jump(c.hour, t.Hour(), func(v int) time.Time { return time.Date(y, mo, d, v, 0, 0, 0, time.UTC) },
				time.Date(y, mo, d+1, 0, 0, 0, 0, time.UTC))
		case !c.minute.matches(t.Minute()):
			t = jump(c.minute, t.Minute(), func(v int) time.Time { return time.Date(y, mo, d, t.Hour(), v, 0, 0, time.UTC) },
				time.Date(y, mo, d, t.Hour()+1, 0, 0, 0, time.UTC))
		case !c.second.matches(t.Second()):
			t = jump(c.second, t.Second(), func(v int) time.Time { return time.Date(y, mo, d, t.Hour(), t.Minute(), v, 0, time.UTC) },
				time.Date(y, mo, d, t.Hour(), t.Minute()+1, 0, 0, time.UTC))
		default:
			return t, true
		}
	}
	return time.Time{}, false
}

// jump moves to the next allowed value of f after v, or to overflow when
// there is none.
func jump(f field, v int, at func(int) time.Time, overflow time.Time) time.Time {
	if nv, ok := f.next(v); ok {
		return at(nv)
	}
	return overflow
}

func (c Calendar) weekdayMatches(t time.Time) bool {
	if c.weekdayAny {
		return true
	}
	return c.weekday[(int(t.Weekday())+6)%7]
}

// field is the set of allowed values of one calendar part.
type field struct {
	any    bool
	values []int // sorted, when !any
}

func (f field) matches(v int) bool { return f.any || slices.Contains(f.values, v) }

// next returns the smallest allowed value above v.
func (f field) next(v int) (int, bool) {
	if f.any {
		return v + 1, true
	}
	for _, x := range f.values {
		if x > v {
			return x, true
		}
	}
	return 0, false
}

func parseField(spec string, lo, hi int) (field, error) {
	if spec == "*" {
		return field{any: true}, nil
	}
	set := map[int]bool{}
	for item := range strings.SplitSeq(spec, ",") {
		if err := addItem(set, item, lo, hi); err != nil {
			return field{}, err
		}
	}
	values := make([]int, 0, len(set))
	for v := range set {
		values = append(values, v)
	}
	slices.Sort(values)
	return field{values: values}, nil
}

func addItem(set map[int]bool, item string, lo, hi int) error {
	rng, stepText, hasStep := strings.Cut(item, "/")
	step := 1
	if hasStep {
		var err error
		if step, err = strconv.Atoi(stepText); err != nil || step < 1 {
			return fmt.Errorf("bad step in %q", item)
		}
	}
	from, to := lo, hi
	switch rng {
	case "*":
		if !hasStep {
			return fmt.Errorf("bad item %q", item)
		}
	default:
		a, b, isRange := strings.Cut(rng, "..")
		var err error
		if from, err = number(a, lo, hi); err != nil {
			return err
		}
		to = from
		if isRange {
			if to, err = number(b, lo, hi); err != nil {
				return err
			}
		} else if hasStep {
			to = hi
		}
		if to < from {
			return fmt.Errorf("range %q runs backwards", item)
		}
	}
	for v := from; v <= to; v += step {
		set[v] = true
	}
	return nil
}

func number(s string, lo, hi int) (int, error) {
	n, err := strconv.Atoi(s)
	if err != nil || s == "" || s[0] == '+' || s[0] == '-' {
		return 0, fmt.Errorf("%q is not a number", s)
	}
	if n < lo || n > hi {
		return 0, fmt.Errorf("%d is outside %d..%d", n, lo, hi)
	}
	return n, nil
}
