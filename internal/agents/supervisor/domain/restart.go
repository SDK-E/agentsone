// SPDX-License-Identifier: MIT

package domain

import "time"

// The restart backoff of a runner that exits with a failure (SDK-2082).
const (
	// RestartFirstDelay is the wait before the first restart of a spell.
	RestartFirstDelay = 2 * time.Second
	// RestartMaxDelay caps the wait, however often the runner failed.
	RestartMaxDelay = 5 * time.Minute
	// RestartStableAfter is how long a restarted runner stays up before
	// the spell of failures is over and the wait starts again at
	// RestartFirstDelay.
	RestartStableAfter = 5 * time.Minute
)

// Backoff decides how long a runner that keeps failing waits before it
// starts again: RestartFirstDelay after the first failure, twice as long
// after each further one, up to RestartMaxDelay. A spell of failures ends
// once the runner stays up for RestartStableAfter. The zero Backoff has no
// failures. It is not safe for concurrent use; the app layer guards it.
type Backoff struct {
	failures int
	up       bool
	upSince  time.Time
}

// Fail records that the runner exited with a failure or could not start.
// It returns how long to wait before starting it again, and whether this
// failure began a spell (the runner was not failing before).
func (b *Backoff) Fail() (wait time.Duration, began bool) {
	began = b.failures == 0
	b.failures++
	b.up = false
	wait = RestartFirstDelay
	for i := 1; i < b.failures && wait < RestartMaxDelay; i++ {
		wait *= 2
	}
	return min(wait, RestartMaxDelay), began
}

// Up records that the runner finished its handshake at now.
func (b *Backoff) Up(now time.Time) { b.up, b.upSince = true, now }

// Failing reports whether a spell of failures is going on.
func (b *Backoff) Failing() bool { return b.failures > 0 }

// Recovered reports whether the spell ended at now: the runner has been up
// for RestartStableAfter since it last came up. The spell ends once, so
// the next failure waits RestartFirstDelay again.
func (b *Backoff) Recovered(now time.Time) bool {
	if b.failures == 0 || !b.up || now.Sub(b.upSince) < RestartStableAfter {
		return false
	}
	*b = Backoff{}
	return true
}
