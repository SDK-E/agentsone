// SPDX-License-Identifier: MIT

package domain

import "time"

// Sign-in throttle: MaxFailures failed sign-ins for one name within
// FailureWindow lock that name until the window has passed.
const (
	MaxFailures   = 5
	FailureWindow = time.Minute
)

// Throttle counts failed sign-ins per name. It is not safe for concurrent
// use; the caller holds a lock.
type Throttle struct{ failures map[string][]time.Time }

// NewThrottle returns an empty throttle.
func NewThrottle() *Throttle { return &Throttle{failures: map[string][]time.Time{}} }

// RetryAfter is zero when name may try now, else how long to wait.
func (t *Throttle) RetryAfter(name string, now time.Time) time.Duration {
	recent := t.recent(name, now)
	if len(recent) < MaxFailures {
		return 0
	}
	return recent[len(recent)-MaxFailures].Add(FailureWindow).Sub(now)
}

// Fail records a failed sign-in.
func (t *Throttle) Fail(name string, now time.Time) {
	t.failures[name] = append(t.recent(name, now), now)
}

// Succeed clears the name's failures.
func (t *Throttle) Succeed(name string) { delete(t.failures, name) }

func (t *Throttle) recent(name string, now time.Time) []time.Time {
	kept := t.failures[name][:0]
	for _, at := range t.failures[name] {
		if now.Sub(at) < FailureWindow {
			kept = append(kept, at)
		}
	}
	if len(kept) == 0 {
		delete(t.failures, name)
		return nil
	}
	t.failures[name] = kept
	return kept
}
