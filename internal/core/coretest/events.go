// SPDX-License-Identifier: MIT

package coretest

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/SDK-E/agentsone/internal/core"
)

// Events is an in-memory bus: core.EventPublisher and core.EventSubscriber.
type Events struct {
	mu        sync.Mutex
	clock     core.Clock
	ids       core.IDs
	published []core.Event
	subs      []*subscription
}

var (
	_ core.EventPublisher  = (*Events)(nil)
	_ core.EventSubscriber = (*Events)(nil)
)

type subscription struct {
	filter core.EventFilter
	ch     chan core.Event
	done   <-chan struct{}
}

// NewEvents returns an empty bus that stamps events with clock and ids.
func NewEvents(clock core.Clock, ids core.IDs) *Events {
	return &Events{clock: clock, ids: ids}
}

// Publish stores e with the next sequence and delivers it to subscribers.
// Actor and ChartScope are required, as in the event schema.
func (b *Events) Publish(_ context.Context, e core.Event) (core.Event, error) {
	if e.Actor == "" || e.ChartScope == "" {
		return core.Event{}, fmt.Errorf("event %q needs actor and chartscope: %w", e.Type, core.ErrInvalid)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	e = b.stamp(e)
	b.published = append(b.published, e)
	for _, s := range b.subs {
		s.offer(e)
	}
	return e, nil
}

func (b *Events) stamp(e core.Event) core.Event {
	e.SpecVersion = "1.0"
	e.Sequence = core.FormatSequence(uint64(len(b.published)) + 1)
	if e.ID == "" {
		e.ID = b.ids.New("evt")
	}
	if e.Time == nil {
		now := b.clock.Now()
		e.Time = &now
	}
	return e
}

// Published returns every stored event in sequence order.
func (b *Events) Published() []core.Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]core.Event(nil), b.published...)
}

// Subscribe replays stored matches after f.AfterSequence ("" replays all),
// then streams new ones until ctx ends.
func (b *Events) Subscribe(ctx context.Context, f core.EventFilter) (<-chan core.Event, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := &subscription{filter: f, ch: make(chan core.Event, len(b.published)+64), done: ctx.Done()}
	for _, e := range b.published {
		s.offer(e)
	}
	b.subs = append(b.subs, s)
	go b.closeWhenDone(ctx, s)
	return s.ch, nil
}

func (b *Events) closeWhenDone(ctx context.Context, s *subscription) {
	<-ctx.Done()
	b.mu.Lock()
	defer b.mu.Unlock()
	for i, other := range b.subs {
		if other == s {
			b.subs = append(b.subs[:i], b.subs[i+1:]...)
			break
		}
	}
	close(s.ch)
}

// offer delivers e when it matches; a full buffer drops it, like a slow client.
func (s *subscription) offer(e core.Event) {
	if !matches(s.filter, e) {
		return
	}
	select {
	case s.ch <- e:
	default:
	}
}

func matches(f core.EventFilter, e core.Event) bool {
	if e.Sequence <= f.AfterSequence || (f.Source != "" && f.Source != e.Source) {
		return false
	}
	if len(f.Types) == 0 {
		return true
	}
	for _, prefix := range f.Types {
		if strings.HasPrefix(e.Type, prefix) {
			return true
		}
	}
	return false
}
