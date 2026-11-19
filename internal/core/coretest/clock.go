// SPDX-License-Identifier: MIT

package coretest

import (
	"sync"
	"time"

	"github.com/SDK-E/agentsone/internal/core"
)

// Epoch is where NewDeps and NewIDs start their clocks.
var Epoch = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// Clock is a core.Clock that only moves when told to.
type Clock struct {
	mu  sync.Mutex
	now time.Time
}

var _ core.Clock = (*Clock)(nil)

// NewClock returns a clock stopped at start.
func NewClock(start time.Time) *Clock { return &Clock{now: start} }

// Now returns the current fake time.
func (c *Clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Advance moves the clock forward by d.
func (c *Clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// Set moves the clock to t.
func (c *Clock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t
}

// NewIDs returns core.IDs that give the same sequence on every run: a clock
// stopped at Epoch and all-zero entropy.
func NewIDs() core.IDs { return core.NewIDs(NewClock(Epoch), zeroReader{}) }

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}
