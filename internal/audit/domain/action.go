// SPDX-License-Identifier: MIT

package domain

import (
	"errors"
	"fmt"
	"strings"
)

// ErrInvalidAction is returned for an action that is not <area>.<verb>.
var ErrInvalidAction = errors.New("invalid audit action")

const maxActionLen = 64

// Action is what was done, as dotted lowercase words: <area>.<verb>, for
// example agent.hire or power.shutdown.
type Action string

// NewAction validates s as an action.
func NewAction(s string) (Action, error) {
	words := strings.Split(s, ".")
	if len(s) > maxActionLen || len(words) < 2 {
		return "", fmt.Errorf("%w: %q", ErrInvalidAction, s)
	}
	for _, w := range words {
		if !isActionWord(w) {
			return "", fmt.Errorf("%w: %q", ErrInvalidAction, s)
		}
	}
	return Action(s), nil
}

func (a Action) String() string { return string(a) }

// isActionWord reports whether w is a lowercase letter followed by
// lowercase letters, digits or hyphens.
func isActionWord(w string) bool {
	if w == "" || !isLower(w[0]) {
		return false
	}
	for i := 1; i < len(w); i++ {
		if c := w[i]; !isLower(c) && !isDigit(c) && c != '-' {
			return false
		}
	}
	return true
}

func isLower(c byte) bool { return c >= 'a' && c <= 'z' }
func isDigit(c byte) bool { return c >= '0' && c <= '9' }
