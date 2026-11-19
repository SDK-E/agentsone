// SPDX-License-Identifier: MIT

// Package domain holds the freeze-all switch as a value: on or off, and since when.
package domain

import "time"

// State is the freeze-all switch. The zero value is off and never changed.
type State struct {
	Frozen bool
	// Since is when the switch last changed; zero when it never did.
	Since time.Time
}

// Change says what a request did to the switch.
type Change int

const (
	// Unchanged means the switch was already in the asked position.
	Unchanged Change = iota
	// Froze means the switch went from off to on.
	Froze
	// Released means the switch went from on to off.
	Released
)

// Freeze turns the switch on at now. Freezing a frozen switch changes nothing, not even Since.
func (s State) Freeze(now time.Time) (State, Change) {
	if s.Frozen {
		return s, Unchanged
	}
	return State{Frozen: true, Since: now}, Froze
}

// Release turns the switch off at now. Releasing an open switch changes nothing.
func (s State) Release(now time.Time) (State, Change) {
	if !s.Frozen {
		return s, Unchanged
	}
	return State{Frozen: false, Since: now}, Released
}
