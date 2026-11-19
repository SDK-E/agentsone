// SPDX-License-Identifier: MIT

// Package app holds the events use cases: publish, query and stream events,
// manage emitters, run triggers and take in webhooks. It depends on the
// domain ports and on core.Ports, never on a concrete adapter.
package app

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/events/domain"
)

// UnitController starts and stops the systemd unit behind a process emitter.
type UnitController interface {
	// Start installs the unit of e when needed and starts it.
	Start(ctx context.Context, e domain.Emitter) error
	// Stop stops the unit of e.
	Stop(ctx context.Context, e domain.Emitter) error
}

// Deps are what the service needs.
type Deps struct {
	Events   domain.EventRepository
	Emitters domain.EmitterRepository
	Triggers domain.TriggerRepository
	Catalog  domain.Catalog
	Units    UnitController
	Clock    core.Clock
	IDs      core.IDs
	// Ports is read at use time: other modules fill it in their Provide.
	Ports *core.Ports
	Log   *slog.Logger
	// Entropy makes webhook secrets. It must be a cryptographic source.
	Entropy io.Reader
}

// Rates the service enforces. They guard against loops and floods.
var (
	publishRate = domain.WakeRate{Max: 600, Window: time.Minute}
	webhookRate = domain.WakeRate{Max: 60, Window: time.Minute}
)

// replayPage is how many stored events one replay read returns.
const replayPage = 500

var systemCaller = core.Caller{Kind: core.CallerSystem, Name: "daemon", IsRoot: true}

// Service is the events bounded context.
type Service struct {
	d Deps
	// mu makes append plus broadcast one step, so a subscriber that
	// registers under mu sees every event exactly once.
	mu      sync.Mutex
	hub     *hub
	limiter *domain.WakeLimiter
}

var (
	_ core.EventPublisher  = (*Service)(nil)
	_ core.EventSubscriber = (*Service)(nil)
)

// New returns the service.
func New(d Deps) *Service {
	return &Service{d: d, hub: newHub(), limiter: domain.NewWakeLimiter(d.Clock)}
}

// callerOf returns the caller in ctx. Work with no caller is the daemon's own.
func callerOf(ctx context.Context) core.Caller {
	if c, ok := core.CallerFrom(ctx); ok {
		return c
	}
	return systemCaller
}

func isPrivileged(c core.Caller) bool {
	return c.IsRoot || c.Kind == core.CallerSystem || c.Kind == core.CallerHost
}

// audit records an action. A failing audit log never fails the action, but
// the failure is logged.
func (s *Service) audit(ctx context.Context, actor, action, target string, detail map[string]any) {
	raw, err := json.Marshal(detail)
	if err != nil {
		raw = json.RawMessage(`{}`)
	}
	err = s.d.Ports.Audit.Append(ctx, core.AuditEntry{Actor: actor, Action: action, Target: target, Detail: raw})
	if err != nil {
		s.d.Log.WarnContext(ctx, "audit append failed", "action", action, "error", err)
	}
}

func actorName(c core.Caller) string { return string(c.Kind) + ":" + c.Name }

// viewerOf says which events and agents the caller may see.
func (s *Service) viewerOf(ctx context.Context, c core.Caller) domain.Viewer {
	switch c.Kind {
	case core.CallerAgent:
		if c.IsRoot {
			return domain.Viewer{All: true}
		}
		root, err := s.d.Ports.Agents.TeamRoot(ctx, c.Name)
		if err != nil {
			root = ""
		}
		return domain.Viewer{Name: c.Name, TeamRoot: root, IsBelow: s.isBelow(ctx, c.Name)}
	case core.CallerPlugin:
		return domain.Viewer{}
	default:
		return domain.Viewer{All: true}
	}
}

func (s *Service) isBelow(ctx context.Context, ancestor string) func(string) bool {
	return func(name string) bool {
		below, err := s.d.Ports.Agents.IsBelow(ctx, name, ancestor)
		return err == nil && below
	}
}

// Publish stores an event for another module and fans it out. The event's
// actor and chart scope are derived, except that the daemon's own modules
// may name the chart scope. See PublishDraft for the API path.
func (s *Service) Publish(ctx context.Context, e core.Event) (core.Event, error) {
	c := callerOf(ctx)
	d := domain.Draft{
		Type: e.Type, Source: e.Source, Subject: e.Subject, Data: e.Data,
		CausationID: e.CausationID, CorrelationID: e.CorrelationID,
	}
	scope := ""
	if isPrivileged(c) {
		scope = e.ChartScope
	}
	return s.publish(ctx, c, d, e.ID, scope)
}

// PublishDraft stores an event for an API caller. The publish is rate
// limited per caller and audited.
func (s *Service) PublishDraft(ctx context.Context, d domain.Draft) (core.Event, error) {
	c := callerOf(ctx)
	actor, err := domain.EventActor(c)
	if err != nil {
		return core.Event{}, err
	}
	if !s.limiter.Allow("publish:"+actor, publishRate) {
		return core.Event{}, core.NewProblem(429, "rate-limited", "too many events published; slow down")
	}
	e, err := s.publish(ctx, c, d, "", "")
	if err != nil {
		return core.Event{}, err
	}
	s.audit(ctx, actorName(c), "event.publish", "event:"+e.ID, map[string]any{"type": e.Type, "source": e.Source})
	return e, nil
}

func (s *Service) publish(ctx context.Context, c core.Caller, d domain.Draft, id, scope string) (core.Event, error) {
	if err := d.Validate(); err != nil {
		return core.Event{}, err
	}
	actor, err := domain.EventActor(c)
	if err != nil {
		return core.Event{}, err
	}
	if d.Source == "" {
		d.Source = domain.DefaultSource(c)
	}
	if err := s.authorise(ctx, c, d); err != nil {
		return core.Event{}, err
	}
	if err := s.d.Catalog.ValidateData(d.Type, d.Data); err != nil {
		return core.Event{}, err
	}
	if scope == "" {
		scope = s.chartScope(ctx, c, d)
	}
	if id == "" {
		id = s.d.IDs.New("evt")
	}
	stored, err := s.store(ctx, d.Event(id, s.d.Clock.Now(), actor, scope))
	if err != nil {
		return core.Event{}, err
	}
	if name, ok := domain.EmitterOfSource(stored.Source); ok {
		if err := s.d.Emitters.Touch(ctx, name, *stored.Time); err != nil {
			s.d.Log.WarnContext(ctx, "emitter touch failed", "emitter", name, "error", err)
		}
	}
	s.fire(ctx, stored)
	return stored, nil
}

// store appends and broadcasts as one step.
func (s *Service) store(ctx context.Context, e core.Event) (core.Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	stored, err := s.d.Events.Append(ctx, e)
	if err != nil {
		return core.Event{}, err
	}
	s.hub.broadcast(stored)
	return stored, nil
}

// authorise checks that c may publish d as d.Source and that the type is open to it.
func (s *Service) authorise(ctx context.Context, c core.Caller, d domain.Draft) error {
	if name, ok := domain.EmitterOfSource(d.Source); ok {
		return s.authoriseEmitter(ctx, c, name, d.Type)
	}
	if !isPrivileged(c) && domain.BuiltinEmitterType(d.Type) {
		return domain.ErrTypeReserved
	}
	switch {
	case isPrivileged(c):
		return nil
	case d.Source == "/agents/"+c.Name && c.Kind == core.CallerAgent:
		return nil
	case d.Source == "/daemon" && c.Kind == core.CallerHuman:
		return nil
	default:
		return domain.ErrSourceForbidden
	}
}

func (s *Service) authoriseEmitter(ctx context.Context, c core.Caller, name, eventType string) error {
	e, err := s.d.Emitters.ByName(ctx, name)
	if err != nil {
		return err
	}
	switch {
	case !e.Enabled:
		return domain.ErrEmitterDisabled
	case !e.MayPublish(c):
		return domain.ErrSourceForbidden
	case !e.Allows(eventType):
		return domain.ErrTypeNotAllowed
	}
	return nil
}

// chartScope decides which part of the chart an event concerns.
func (s *Service) chartScope(ctx context.Context, c core.Caller, d domain.Draft) string {
	kind, known := s.d.Catalog.ScopeKind(d.Type)
	if !known {
		if c.Kind != core.CallerAgent || !domain.ValidName(c.Name) {
			return "machine"
		}
		return s.teamScope(ctx, c.Name, "agent:"+c.Name)
	}
	agent := agentOf(d.Data)
	switch {
	case kind == "agent" && domain.ValidName(agent):
		return "agent:" + agent
	case kind == "team" && domain.ValidName(agent):
		return s.teamScope(ctx, agent, "machine")
	default:
		return "machine"
	}
}

func (s *Service) teamScope(ctx context.Context, agent, fallback string) string {
	root, err := s.d.Ports.Agents.TeamRoot(ctx, agent)
	if err != nil || !domain.ValidName(root) {
		return fallback
	}
	return "team:" + root
}

// agentOf reads the "agent" field that agent-scoped catalog events carry.
func agentOf(data json.RawMessage) string {
	var v struct {
		Agent string `json:"agent"`
	}
	if err := json.Unmarshal(data, &v); err != nil {
		return ""
	}
	return v.Agent
}

// Query selects stored events.
type Query struct {
	Filter domain.Filter
	// After is the sequence to start after. Zero starts at the oldest.
	After uint64
	Limit int
}

// Page is one page of events and the sequence to resume from.
type Page struct {
	Events []core.Event
	// Next is the sequence to pass as After for the next page, or "" at the end.
	Next string
}

// List returns stored events the caller may see, oldest first.
func (s *Service) List(ctx context.Context, q Query) (Page, error) {
	viewer := s.viewerOf(ctx, callerOf(ctx))
	var page Page
	cursor := q.After
	for {
		batch, err := s.d.Events.After(ctx, cursor, replayPage)
		if err != nil {
			return Page{}, err
		}
		for _, e := range batch {
			seq, err := domain.ParseSequence(e.Sequence)
			if err != nil {
				return Page{}, err
			}
			cursor = seq
			if !q.Filter.Matches(e) || !viewer.CanSee(e) {
				continue
			}
			if len(page.Events) == q.Limit {
				page.Next = core.FormatSequence(last(page.Events))
				return page, nil
			}
			page.Events = append(page.Events, e)
		}
		if len(batch) < replayPage {
			return page, nil
		}
	}
}

func last(events []core.Event) uint64 {
	seq, _ := domain.ParseSequence(events[len(events)-1].Sequence)
	return seq
}

// Stream returns events the caller may see as they happen. With after set,
// stored events after that sequence come first.
func (s *Service) Stream(ctx context.Context, f domain.Filter, after *uint64) (<-chan core.Event, error) {
	viewer := s.viewerOf(ctx, callerOf(ctx))
	return s.stream(ctx, func(e core.Event) bool { return f.Matches(e) && viewer.CanSee(e) }, after)
}

// Subscribe implements core.EventSubscriber for other modules, which see
// every event. An empty AfterSequence replays everything stored.
func (s *Service) Subscribe(ctx context.Context, f core.EventFilter) (<-chan core.Event, error) {
	var after uint64
	if f.AfterSequence != "" {
		n, err := domain.ParseSequence(f.AfterSequence)
		if err != nil {
			return nil, err
		}
		after = n
	}
	match := func(e core.Event) bool {
		if f.Source != "" && e.Source != f.Source {
			return false
		}
		return domain.MatchAny(prefixes(f.Types), e.Type)
	}
	return s.stream(ctx, match, &after)
}

// prefixes turns type prefixes into patterns.
func prefixes(types []string) []string {
	out := make([]string, len(types))
	for i, t := range types {
		out[i] = t + "*"
	}
	return out
}

func (s *Service) stream(ctx context.Context, match func(core.Event) bool, after *uint64) (<-chan core.Event, error) {
	s.mu.Lock()
	cutoff, err := s.d.Events.Last(ctx)
	if err != nil {
		s.mu.Unlock()
		return nil, err
	}
	live := s.hub.add()
	s.mu.Unlock()

	out := make(chan core.Event, 16)
	go s.pump(ctx, live, match, after, cutoff, out)
	return out, nil
}

// pump sends the replay, then the live events, to out until ctx ends or the
// hub drops the subscriber.
func (s *Service) pump(ctx context.Context, live *subscriber, match func(core.Event) bool, after *uint64, cutoff uint64, out chan<- core.Event) {
	defer close(out)
	defer s.hub.remove(live)
	send := func(e core.Event) bool {
		select {
		case out <- e:
			return true
		case <-ctx.Done():
			return false
		}
	}
	if after != nil && !s.replay(ctx, match, *after, cutoff, send) {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case e, ok := <-live.ch:
			if !ok || (match(e) && !send(e)) {
				return
			}
		}
	}
}

// replay sends stored events in (after, cutoff]. It reports false when the
// stream must end.
func (s *Service) replay(ctx context.Context, match func(core.Event) bool, after, cutoff uint64, send func(core.Event) bool) bool {
	cursor := after
	for cursor < cutoff {
		batch, err := s.d.Events.After(ctx, cursor, replayPage)
		if err != nil {
			s.d.Log.WarnContext(ctx, "event replay failed", "error", err)
			return false
		}
		if len(batch) == 0 {
			return true
		}
		for _, e := range batch {
			seq, err := domain.ParseSequence(e.Sequence)
			if err != nil || seq > cutoff {
				return err == nil
			}
			cursor = seq
			if match(e) && !send(e) {
				return false
			}
		}
	}
	return true
}
