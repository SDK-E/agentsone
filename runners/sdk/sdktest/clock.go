// SPDX-License-Identifier: MIT

// Package sdktest has fakes for testing runners built on the SDK.
package sdktest

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/SDK-E/agentsone/runners/sdk"
)

// Clock is a fake sdk.Clock. Time moves only when Advance is called.
type Clock struct {
	mu      sync.Mutex
	now     time.Time
	tickers []*ticker
	afters  []*after
	// Registered receives one value each time the code under test creates a
	// ticker or an after timer, so a test can wait for it without sleeping.
	Registered chan struct{}
}

var _ sdk.Clock = (*Clock)(nil)

// NewClock returns a fake clock that starts at start.
func NewClock(start time.Time) *Clock {
	return &Clock{now: start, Registered: make(chan struct{}, 64)}
}

// Now returns the fake time.
func (c *Clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

type ticker struct {
	c      chan time.Time
	period time.Duration
	next   time.Time
	stop   atomic.Bool
}

func (t *ticker) C() <-chan time.Time { return t.c }

// Stop ends the ticker.
func (t *ticker) Stop() { t.stop.Store(true) }

type after struct {
	c  chan time.Time
	at time.Time
}

// NewTicker returns a ticker that fires when Advance passes a multiple of d.
func (c *Clock) NewTicker(d time.Duration) sdk.Ticker {
	c.mu.Lock()
	t := &ticker{c: make(chan time.Time, 1), period: d, next: c.now.Add(d)}
	c.tickers = append(c.tickers, t)
	c.mu.Unlock()
	c.Registered <- struct{}{}
	return t
}

// After fires once, when Advance passes d.
func (c *Clock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	a := &after{c: make(chan time.Time, 1), at: c.now.Add(d)}
	c.afters = append(c.afters, a)
	c.mu.Unlock()
	c.Registered <- struct{}{}
	return a.c
}

// Advance moves the time forward and fires what is due.
func (c *Clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	for _, t := range c.tickers {
		for !t.stop.Load() && !t.next.After(c.now) {
			select {
			case t.c <- t.next:
			default: // a slow reader misses ticks, like a real ticker
			}
			t.next = t.next.Add(t.period)
		}
	}
	for _, a := range c.afters {
		if a.c != nil && !a.at.After(c.now) {
			a.c <- a.at
			a.c = nil
		}
	}
}
