// SPDX-License-Identifier: MIT

package domain

import (
	"strconv"
	"strings"
)

// MatchPattern reports whether value matches pattern. A pattern ending in
// "*" or "." matches every value that starts with the text before the
// ending ("agentsone.disk.*" matches "agentsone.disk.usage.high"); any
// other pattern must equal the value.
func MatchPattern(pattern, value string) bool {
	switch {
	case strings.HasSuffix(pattern, "*"):
		return strings.HasPrefix(value, strings.TrimSuffix(pattern, "*"))
	case strings.HasSuffix(pattern, "."):
		return strings.HasPrefix(value, pattern)
	default:
		return pattern == value
	}
}

// MatchAny reports whether value matches any of patterns. No patterns match everything.
func MatchAny(patterns []string, value string) bool {
	if len(patterns) == 0 {
		return true
	}
	for _, p := range patterns {
		if MatchPattern(p, value) {
			return true
		}
	}
	return false
}

// ParseSequence reads a bus sequence ("" is zero).
func ParseSequence(s string) (uint64, error) {
	if s == "" {
		return 0, nil
	}
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0, ErrInvalidSequence
	}
	return n, nil
}
