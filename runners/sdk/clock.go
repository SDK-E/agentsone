// SPDX-License-Identifier: MIT

package sdk

import "time"

// Clock is the time port. Tests use a fake one (package sdktest); a runner
// main uses SystemClock.
type Clock interface {
	Now() time.Time
	// NewTicker returns a ticker that fires every d.
	NewTicker(d time.Duration) Ticker
	// After fires once after d.
	After(d time.Duration) <-chan time.Time
}

// Ticker is the part of time.Ticker the SDK uses.
type Ticker interface {
	C() <-chan time.Time
	Stop()
}

// SystemClock is the real clock.
type SystemClock struct{}

// Now returns the current time.
func (SystemClock) Now() time.Time { return time.Now().UTC() }

// NewTicker wraps time.NewTicker.
func (SystemClock) NewTicker(d time.Duration) Ticker { return sysTicker{time.NewTicker(d)} }

// After wraps time.After.
func (SystemClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

type sysTicker struct{ t *time.Ticker }

func (s sysTicker) C() <-chan time.Time { return s.t.C }
func (s sysTicker) Stop()               { s.t.Stop() }
