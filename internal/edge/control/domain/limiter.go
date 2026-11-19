// SPDX-License-Identifier: MIT

package domain

import (
	"sync"
	"time"
)

// Limiter slows down guessing of the passphrase and the setup code. The control page has one
// human, so it counts failures for the whole page, not per address: behind a proxy every visitor
// shares one address, and an attacker can change theirs. After the free failures, each failure
// locks the page for twice as long as the one before, up to a ceiling. A success clears it.
//
// The ceiling is short on purpose (the control app uses one minute): someone who can reach the
// page can make the human wait, but never longer than the ceiling, while one guess per ceiling
// against a passphrase of at least 12 characters gets an attacker nowhere.
type Limiter struct {
	free          int
	base, ceiling time.Duration

	mu       sync.Mutex
	failures int
	until    time.Time
}

// NewLimiter allows free failures, then locks for base, doubling up to ceiling.
func NewLimiter(free int, base, ceiling time.Duration) *Limiter {
	return &Limiter{free: free, base: base, ceiling: ceiling}
}

// Wait is how long until the next try is allowed; zero means now.
func (l *Limiter) Wait(now time.Time) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !now.Before(l.until) {
		return 0
	}
	return l.until.Sub(now)
}

// Fail records a failed try at now.
func (l *Limiter) Fail(now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.failures++
	over := l.failures - l.free
	if over <= 0 {
		return
	}
	lock := l.ceiling
	if over <= 30 { // 1<<30 base seconds is past any sensible ceiling; avoid overflow
		if d := l.base << (over - 1); d > 0 && d < l.ceiling {
			lock = d
		}
	}
	l.until = now.Add(lock)
}

// Succeed clears the failures.
func (l *Limiter) Succeed() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.failures = 0
	l.until = time.Time{}
}
