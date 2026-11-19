// SPDX-License-Identifier: MIT

// Package nettest holds in-memory fakes for the ports of the edge internet filter.
package nettest

import (
	"context"
	"fmt"
	"net/netip"
	"slices"
	"sync"
	"time"

	"github.com/SDK-E/agentsone/internal/edge/net/app"
	"github.com/SDK-E/agentsone/internal/edge/net/domain"
)

// Clock is a fake clock moved by hand.
type Clock struct {
	mu  sync.Mutex
	now time.Time
}

// NewClock starts a fake clock at now.
func NewClock(now time.Time) *Clock { return &Clock{now: now} }

// Now returns the fake time.
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

// Resolver is a fake upstream resolver with fixed answers.
type Resolver struct {
	mu      sync.Mutex
	answers map[string][]netip.Addr
	fail    error
	asked   []string
}

// NewResolver makes a resolver that knows no names.
func NewResolver() *Resolver { return &Resolver{answers: map[string][]netip.Addr{}} }

// Set makes host resolve to addrs.
func (r *Resolver) Set(host string, addrs ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	parsed := make([]netip.Addr, 0, len(addrs))
	for _, a := range addrs {
		parsed = append(parsed, netip.MustParseAddr(a))
	}
	r.answers[host] = parsed
}

// FailWith makes every lookup fail with err until called with nil.
func (r *Resolver) FailWith(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.fail = err
}

// Asked returns every name looked up so far, in order.
func (r *Resolver) Asked() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.asked)
}

// LookupIPv4 answers from the fixed answers, like a real resolver would.
func (r *Resolver) LookupIPv4(_ context.Context, host string) ([]netip.Addr, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.asked = append(r.asked, host)
	if r.fail != nil {
		return nil, r.fail
	}
	addrs, ok := r.answers[host]
	if !ok {
		return nil, fmt.Errorf("lookup %s: %w", host, app.ErrNameNotFound)
	}
	return slices.Clone(addrs), nil
}

// Observer records audit events.
type Observer struct {
	mu     sync.Mutex
	events []app.Event
	notify chan struct{}
}

// NewObserver makes an empty observer.
func NewObserver() *Observer { return &Observer{notify: make(chan struct{}, 1)} }

// Record keeps e.
func (o *Observer) Record(e app.Event) {
	o.mu.Lock()
	o.events = append(o.events, e)
	o.mu.Unlock()
	select {
	case o.notify <- struct{}{}:
	default:
	}
}

// Events returns the events recorded so far.
func (o *Observer) Events() []app.Event {
	o.mu.Lock()
	defer o.mu.Unlock()
	return slices.Clone(o.events)
}

// Wait returns the first event that matches, waiting for it until ctx is done.
func (o *Observer) Wait(ctx context.Context, match func(app.Event) bool) (app.Event, error) {
	for {
		for _, e := range o.Events() {
			if match(e) {
				return e, nil
			}
		}
		select {
		case <-o.notify:
		case <-ctx.Done():
			return app.Event{}, fmt.Errorf("no matching event: %w", ctx.Err())
		}
	}
}

// Store is an in-memory allow list store.
type Store struct {
	mu       sync.Mutex
	list     domain.AllowList
	exists   bool
	failLoad error
}

// NewStore makes a store with no list saved yet.
func NewStore() *Store { return &Store{} }

// Init saves the default list when none exists.
func (s *Store) Init(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.exists {
		s.list, s.exists = domain.DefaultAllowList(), true
	}
	return nil
}

// Load returns the saved list, or the default list when none is saved.
func (s *Store) Load(context.Context) (domain.AllowList, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failLoad != nil {
		return domain.AllowList{}, s.failLoad
	}
	if !s.exists {
		return domain.DefaultAllowList(), nil
	}
	return s.list, nil
}

// Update applies change and saves the result.
func (s *Store) Update(_ context.Context, change func(domain.AllowList) (domain.AllowList, error)) (domain.AllowList, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current := s.list
	if !s.exists {
		current = domain.DefaultAllowList()
	}
	next, err := change(current)
	if err != nil {
		return domain.AllowList{}, err
	}
	s.list, s.exists = next, true
	return next, nil
}

// FailLoadWith makes Load fail with err (nil clears it), like a malformed file on disk.
func (s *Store) FailLoadWith(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failLoad = err
}

// FreezeStore is an in-memory freeze switch.
type FreezeStore struct {
	mu       sync.Mutex
	frozen   bool
	failLoad error
	failSave error
}

// NewFreezeStore makes a store that is not frozen.
func NewFreezeStore() *FreezeStore { return &FreezeStore{} }

// Load returns the saved switch.
func (s *FreezeStore) Load(context.Context) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failLoad != nil {
		return false, s.failLoad
	}
	return s.frozen, nil
}

// Save stores the switch.
func (s *FreezeStore) Save(_ context.Context, frozen bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failSave != nil {
		return s.failSave
	}
	s.frozen = frozen
	return nil
}

// FailLoadWith makes Load fail with err (nil clears it).
func (s *FreezeStore) FailLoadWith(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failLoad = err
}

// FailSaveWith makes Save fail with err (nil clears it).
func (s *FreezeStore) FailSaveWith(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failSave = err
}

// Watcher is a fake watcher triggered by hand.
type Watcher struct {
	changes chan chan struct{}
	started chan struct{}
	once    sync.Once
}

// NewWatcher makes a watcher.
func NewWatcher() *Watcher {
	return &Watcher{changes: make(chan chan struct{}), started: make(chan struct{})}
}

// Watch delivers Trigger calls to onChange until ctx is done.
func (w *Watcher) Watch(ctx context.Context, onChange func()) error {
	w.once.Do(func() { close(w.started) })
	for {
		select {
		case <-ctx.Done():
			return nil
		case done := <-w.changes:
			onChange()
			close(done)
		}
	}
}

// Trigger reports a change and returns once onChange has run.
func (w *Watcher) Trigger(ctx context.Context) error {
	select {
	case <-w.started:
	case <-ctx.Done():
		return ctx.Err()
	}
	done := make(chan struct{})
	select {
	case w.changes <- done:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
