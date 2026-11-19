// SPDX-License-Identifier: MIT

package domain

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Micros is an amount of US dollars in millionths of a dollar. All budget
// arithmetic uses whole micro-dollars, never floating point.
type Micros int64

// MicrosPerUSD is how many micro-dollars make one dollar.
const MicrosPerUSD Micros = 1_000_000

// ErrBadAmount is returned by ParseUSD for text that is not a dollar amount.
var ErrBadAmount = errors.New("budget: not a dollar amount")

// maxWholeDollars keeps parsed amounts far from int64 overflow.
const maxWholeDollars = math.MaxInt64 / int64(MicrosPerUSD) / 1000

// ParseUSD reads a non-negative dollar amount such as "12", "12.50",
// "$1,000.25" or "0.000001". At most six decimal places are allowed.
func ParseUSD(s string) (Micros, error) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "$")
	s = strings.ReplaceAll(s, ",", "")
	if s == "" {
		return 0, fmt.Errorf("%w: empty", ErrBadAmount)
	}
	whole, frac, _ := strings.Cut(s, ".")
	if whole == "" {
		whole = "0"
	}
	if len(frac) > 6 {
		return 0, fmt.Errorf("%w: more than six decimal places in %q", ErrBadAmount, s)
	}
	if !allDigits(whole) || !allDigits(frac) {
		return 0, fmt.Errorf("%w: %q", ErrBadAmount, s)
	}
	w, err := strconv.ParseInt(whole, 10, 64)
	if err != nil || w > maxWholeDollars {
		return 0, fmt.Errorf("%w: %q is too large", ErrBadAmount, s)
	}
	var f int64
	if frac != "" {
		f, _ = strconv.ParseInt(frac+strings.Repeat("0", 6-len(frac)), 10, 64)
	}
	return Micros(w)*MicrosPerUSD + Micros(f), nil
}

func allDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// String formats the amount as dollars, with cents always shown and smaller
// fractions only when present: "$12.50", "$0.000001".
func (m Micros) String() string {
	sign := ""
	if m < 0 {
		sign = "-"
		m = -m
	}
	whole := m / MicrosPerUSD
	frac := fmt.Sprintf("%06d", int64(m%MicrosPerUSD))
	frac = strings.TrimRight(frac, "0")
	for len(frac) < 2 {
		frac += "0"
	}
	return fmt.Sprintf("%s$%d.%s", sign, int64(whole), frac)
}
