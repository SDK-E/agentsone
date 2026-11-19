// SPDX-License-Identifier: MIT

package core

import "time"

// Clock tells the time. Modules use Deps.Clock instead of time.Now so tests
// can control time.
type Clock interface {
	Now() time.Time
}

// SystemClock is the real clock, always in UTC.
type SystemClock struct{}

// Now returns the current UTC time.
func (SystemClock) Now() time.Time { return time.Now().UTC() }
