// SPDX-License-Identifier: MIT

package domain

import (
	"sync"
	"time"
)

// BreakerState is the state of one provider's circuit breaker.
type BreakerState int

// Breaker states.
const (
	// BreakerClosed lets calls through.
	BreakerClosed BreakerState = iota
	// BreakerOpen refuses calls until the cooldown ends.
	BreakerOpen
	// BreakerHalfOpen lets one probe call through.
	BreakerHalfOpen
)

func (s BreakerState) String() string {
	switch s {
	case BreakerOpen:
		return "open"
	case BreakerHalfOpen:
		return "half-open"
	default:
		return "closed"
	}
}

type breakerEntry struct {
	failures  int
	open      bool
	openUntil time.Time
	probing   bool
}

// Breaker is a circuit breaker per provider. After threshold failures in a
// row (errors, timeouts, server errors) a provider is skipped for cooldown;
// then one probe call is let through, and its result closes or reopens the
// breaker. Time is passed in, so the breaker is deterministic. It is safe for
// concurrent use.
type Breaker struct {
	threshold int
	cooldown  time.Duration

	mu      sync.Mutex
	entries map[ProviderID]*breakerEntry
}

// NewBreaker returns a breaker that opens after threshold failures in a row
// and stays open for cooldown.
func NewBreaker(threshold int, cooldown time.Duration) *Breaker {
	return &Breaker{threshold: max(threshold, 1), cooldown: cooldown, entries: map[ProviderID]*breakerEntry{}}
}

func (b *Breaker) entry(p ProviderID) *breakerEntry {
	e, ok := b.entries[p]
	if !ok {
		e = &breakerEntry{}
		b.entries[p] = e
	}
	return e
}

func (b *Breaker) state(e *breakerEntry, now time.Time) BreakerState {
	switch {
	case !e.open:
		return BreakerClosed
	case now.Before(e.openUntil):
		return BreakerOpen
	default:
		return BreakerHalfOpen
	}
}

// State returns the provider's state at now.
func (b *Breaker) State(p ProviderID, now time.Time) BreakerState {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.state(b.entry(p), now)
}

// Healthy reports whether the provider may be ranked: closed or half-open.
func (b *Breaker) Healthy(p ProviderID, now time.Time) bool { return b.State(p, now) != BreakerOpen }

// Allow reports whether a call may go to the provider now. In half-open state
// only one probe is allowed until its result is reported.
func (b *Breaker) Allow(p ProviderID, now time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	e := b.entry(p)
	switch b.state(e, now) {
	case BreakerClosed:
		return true
	case BreakerHalfOpen:
		if e.probing {
			return false
		}
		e.probing = true
		return true
	default:
		return false
	}
}

// Success records a good call and closes the breaker.
func (b *Breaker) Success(p ProviderID) {
	b.mu.Lock()
	defer b.mu.Unlock()
	*b.entry(p) = breakerEntry{}
}

// Abandon reports a call that was allowed but never reached the provider (the
// caller went away, or no key is set). It frees a half-open probe and leaves
// everything else as it was.
func (b *Breaker) Abandon(p ProviderID) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.entry(p).probing = false
}

// Failure records a failed call at now.
func (b *Breaker) Failure(p ProviderID, now time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()
	e := b.entry(p)
	e.failures++
	if e.open || e.failures >= b.threshold {
		*e = breakerEntry{open: true, openUntil: now.Add(b.cooldown)}
	}
}
