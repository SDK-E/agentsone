// SPDX-License-Identifier: MIT

package domain

import (
	"sync"
	"time"

	"github.com/SDK-E/agentsone/internal/core"
)

// WakeRate allows at most Max firings per Window.
type WakeRate struct {
	Max    int
	Window time.Duration
}

// WakeLimiter is a sliding-window limiter, one window per key (a trigger id).
// A firing stops counting exactly one Window after it happened.
type WakeLimiter struct {
	clock core.Clock
	mu    sync.Mutex
	fires map[string][]time.Time
}

// NewWakeLimiter returns an empty limiter on clock.
func NewWakeLimiter(clock core.Clock) *WakeLimiter {
	return &WakeLimiter{clock: clock, fires: map[string][]time.Time{}}
}

// Allow records a firing for key and returns true when rate has room. A refusal records nothing.
func (l *WakeLimiter) Allow(key string, rate WakeRate) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.clock.Now()
	var live []time.Time
	for _, at := range l.fires[key] {
		if now.Sub(at) < rate.Window {
			live = append(live, at)
		}
	}
	if len(live) >= rate.Max {
		l.fires[key] = live
		return false
	}
	l.fires[key] = append(live, now)
	return true
}

// Forget drops the window of key, for a deleted trigger.
func (l *WakeLimiter) Forget(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.fires, key)
}
