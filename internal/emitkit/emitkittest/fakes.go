// SPDX-License-Identifier: MIT

// Package emitkittest holds the hand-written fakes every event source test
// uses: a clock that only moves when a sleep moves it, a publisher that
// records drafts, and a quiet logger.
package emitkittest

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/SDK-E/agentsone/internal/emitkit/app"
	"github.com/SDK-E/agentsone/internal/emitkit/domain"
)

// Clock is a manual clock.
type Clock struct {
	mu  sync.Mutex
	now time.Time
}

// NewClock returns a clock set to t.
func NewClock(t time.Time) *Clock { return &Clock{now: t} }

// Now returns the fake time.
func (c *Clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Advance moves the fake time forward.
func (c *Clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// Sleeper sleeps by advancing its clock, so a test never waits. After each
// sleep it calls OnSleep, where a test can cancel the context to stop a loop.
type Sleeper struct {
	Clock   *Clock
	OnSleep func(d time.Duration)

	mu     sync.Mutex
	slept  []time.Duration
	Cancel context.CancelFunc
	Limit  int
}

// Sleep advances the clock by d and returns the context's error, if any.
func (s *Sleeper) Sleep(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.Clock != nil {
		s.Clock.Advance(d)
	}
	s.mu.Lock()
	s.slept = append(s.slept, d)
	n := len(s.slept)
	s.mu.Unlock()
	if s.OnSleep != nil {
		s.OnSleep(d)
	}
	if s.Cancel != nil && s.Limit > 0 && n >= s.Limit {
		s.Cancel()
	}
	return ctx.Err()
}

// Slept returns every duration slept so far.
func (s *Sleeper) Slept() []time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]time.Duration(nil), s.slept...)
}

// Publisher records drafts. Fail, when set, decides the error for call n
// (counting from 1); nil means success.
type Publisher struct {
	Fail func(n int) error

	mu     sync.Mutex
	drafts []domain.Draft
	calls  int
}

// Publish records the draft unless Fail says otherwise.
func (p *Publisher) Publish(_ context.Context, d domain.Draft) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	if p.Fail != nil {
		if err := p.Fail(p.calls); err != nil {
			return err
		}
	}
	p.drafts = append(p.drafts, d)
	return nil
}

// Drafts returns the drafts that were accepted.
func (p *Publisher) Drafts() []domain.Draft {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]domain.Draft(nil), p.drafts...)
}

// Calls returns how many times Publish was called, failures included.
func (p *Publisher) Calls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

// Logger returns a logger that drops everything.
func Logger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// Deps wires the fakes into the dependencies a source runs with.
func Deps(clock *Clock, sleeper *Sleeper, pub *Publisher) app.Deps {
	return app.Deps{Publisher: pub, Clock: clock, Sleeper: sleeper, Log: Logger()}
}
