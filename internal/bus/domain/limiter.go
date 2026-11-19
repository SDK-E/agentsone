// SPDX-License-Identifier: MIT

package domain

import (
	"sync"
	"time"

	"github.com/SDK-E/agentsone/internal/core"
)

// Rate allows at most Max sends per Window.
type Rate struct {
	Max    int
	Window time.Duration
}

// Limiter is a sliding-window limiter with one window per key (a sender). A
// send stops counting exactly one Window after it happened.
type Limiter struct {
	clock core.Clock
	mu    sync.Mutex
	sends map[string][]time.Time
}

// NewLimiter returns an empty limiter on clock.
func NewLimiter(clock core.Clock) *Limiter {
	return &Limiter{clock: clock, sends: map[string][]time.Time{}}
}

// Allow records a send for key and returns true when rate has room. A refusal records nothing.
func (l *Limiter) Allow(key string, rate Rate) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.clock.Now()
	var live []time.Time
	for _, at := range l.sends[key] {
		if now.Sub(at) < rate.Window {
			live = append(live, at)
		}
	}
	if len(live) >= rate.Max {
		l.sends[key] = live
		return false
	}
	l.sends[key] = append(live, now)
	return true
}
