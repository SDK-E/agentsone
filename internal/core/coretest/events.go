// SPDX-License-Identifier: MIT

package coretest

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/events/adapters/catalog"
	eventsdomain "github.com/SDK-E/agentsone/internal/events/domain"
)

// builtinCatalog is the contract event catalog the events module validates
// against, compiled once for every test in the binary.
var builtinCatalog = sync.OnceValues(catalog.Builtin)

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
// Actor and ChartScope are required, as in the event schema. It refuses
// what the events module refuses when a daemon module publishes, with the
// events module's own rules and the contract event catalog, so a test that
// publishes through it proves the event would be stored.
func (b *Events) Publish(ctx context.Context, e core.Event) (core.Event, error) {
	if e.Actor == "" || e.ChartScope == "" {
		return core.Event{}, fmt.Errorf("event %q needs actor and chartscope: %w", e.Type, core.ErrInvalid)
	}
	if err := validate(ctx, e); err != nil {
		return core.Event{}, fmt.Errorf("publish %s: %w", e.Type, err)
	}
	actor, err := portActor(ctx, e.Actor)
	if err != nil {
		return core.Event{}, fmt.Errorf("publish %s: %w", e.Type, err)
	}
	e.Actor = actor
	b.mu.Lock()
	defer b.mu.Unlock()
	e = b.stamp(e)
	b.published = append(b.published, e)
	for _, s := range b.subs {
		s.offer(e)
	}
	return e, nil
}

// validate checks e as the events module does: the caller must be one it can
// name as the actor and may publish the type as the source, the envelope must
// pass the draft rules, and the data must match the type's schema in the
// catalog. Daemon modules publish catalog types only, so a type outside the
// catalog, or a chart scope of another kind than the catalog gives the type,
// is refused too. An emitter source is not checked: its rules need the
// emitter records, which live in the events module.
func validate(ctx context.Context, e core.Event) error {
	if c, ok := core.CallerFrom(ctx); ok {
		if _, err := eventsdomain.EventActor(c); err != nil {
			return fmt.Errorf("caller %s:%q: %w", c.Kind, c.Name, err)
		}
		if _, emitter := eventsdomain.EmitterOfSource(e.Source); !emitter {
			if err := eventsdomain.AuthoriseSource(c, e.Source, e.Type); err != nil {
				return fmt.Errorf("caller %s:%q as %s: %w", c.Kind, c.Name, e.Source, err)
			}
		}
	}
	d := eventsdomain.Draft{
		Type: e.Type, Source: e.Source, Subject: e.Subject, Data: e.Data,
		CausationID: e.CausationID, CorrelationID: e.CorrelationID,
	}
	if err := d.Validate(); err != nil {
		return fmt.Errorf("envelope: %w", err)
	}
	cat, err := builtinCatalog()
	if err != nil {
		return fmt.Errorf("load the event catalog: %w", err)
	}
	kind, ok := cat.ScopeKind(e.Type)
	if !ok {
		return fmt.Errorf("%w: type is not in the contract event catalog", core.ErrInvalid)
	}
	if got, _, _ := strings.Cut(e.ChartScope, ":"); got != kind {
		return fmt.Errorf("%w: chart scope %q is not of kind %q, as the catalog gives the type", core.ErrInvalid, e.ChartScope, kind)
	}
	if err := cat.ValidateData(e.Type, e.Data); err != nil {
		return fmt.Errorf("data: %w", err)
	}
	return nil
}

// portActor writes the actor as the events module does: a daemon module names
// it, anyone else is the actor. Work with no caller is the daemon's own.
func portActor(ctx context.Context, named string) (string, error) {
	c, ok := core.CallerFrom(ctx)
	if !ok {
		c = core.Caller{Kind: core.CallerSystem, Name: "daemon", IsRoot: true}
	}
	actor, err := eventsdomain.PortActor(c, named)
	if err != nil {
		return "", fmt.Errorf("actor %q: %w", named, err)
	}
	return actor, nil
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
