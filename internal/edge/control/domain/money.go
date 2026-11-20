// SPDX-License-Identifier: MIT

package domain

import (
	"errors"
	"strconv"
	"strings"
)

// Errors of the budget inputs.
var (
	ErrAmount         = errors.New("write the amount in dollars, like 25 or 25.50")
	ErrAmountTooLarge = errors.New("the amount is larger than one billion dollars")
	ErrShare          = errors.New("write the share as a whole number from 0 to 100")
	ErrPeriod         = errors.New("choose day, week or month")
)

const (
	microsPerDollar = 1_000_000
	maxDollars      = 1_000_000_000
)

// ParseDollars reads a dollar amount, like "25", "$1,250.75" or "0.000001", into millionths of a
// dollar (micros), the unit of the edge budget. Negative amounts and more than six decimals are
// refused.
func ParseDollars(in string) (int64, error) {
	s := strings.TrimPrefix(strings.TrimSpace(in), "$")
	s = strings.ReplaceAll(s, ",", "")
	whole, frac, hasDot := strings.Cut(s, ".")
	if !allDigits(whole) || (hasDot && !allDigits(frac)) || len(frac) > 6 {
		return 0, ErrAmount
	}
	dollars, err := strconv.ParseInt(whole, 10, 64)
	if err != nil || dollars > maxDollars {
		return 0, ErrAmountTooLarge
	}
	micros := dollars * microsPerDollar
	if frac != "" {
		f, _ := strconv.ParseInt(frac+strings.Repeat("0", 6-len(frac)), 10, 64) //nolint:errcheck // the checks above leave one to six digits, which always parse
		micros += f
	}
	if micros > maxDollars*microsPerDollar {
		return 0, ErrAmountTooLarge
	}
	return micros, nil
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// FormatDollars writes micros as dollars with thousands separators: two decimals, or up to six
// when the amount has a fraction of a cent.
func FormatDollars(micros int64) string {
	sign := ""
	if micros < 0 {
		sign, micros = "-", -micros
	}
	whole := strconv.FormatInt(micros/microsPerDollar, 10)
	var b strings.Builder
	for i, r := range whole {
		if i > 0 && (len(whole)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	frac := strconv.FormatInt(micros%microsPerDollar+microsPerDollar, 10)[1:]
	frac = strings.TrimRight(frac, "0")
	for len(frac) < 2 {
		frac += "0"
	}
	return sign + "$" + b.String() + "." + frac
}

// ParseShare reads a whole percentage from 0 to 100; a trailing % is allowed.
func ParseShare(in string) (int, error) {
	s := strings.TrimSuffix(strings.TrimSpace(in), "%")
	if !allDigits(s) {
		return 0, ErrShare
	}
	n, err := strconv.Atoi(s)
	if err != nil || n > 100 {
		return 0, ErrShare
	}
	return n, nil
}

// Period is how often the total budget starts again.
type Period string

// The budget periods the edge knows.
const (
	PeriodDay   Period = "day"
	PeriodWeek  Period = "week"
	PeriodMonth Period = "month"
)

// ParsePeriod reads a budget period.
func ParsePeriod(in string) (Period, error) {
	switch p := Period(in); p {
	case PeriodDay, PeriodWeek, PeriodMonth:
		return p, nil
	default:
		return "", ErrPeriod
	}
}
