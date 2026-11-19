// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"sync"
	"time"

	"github.com/SDK-E/agentsone/internal/edge/net/domain"
)

// Defaults used when a FilterConfig field is zero.
const (
	// DefaultAnswerTTL is the TTL on DNS answers to the machine.
	DefaultAnswerTTL = time.Minute
	// DefaultResolveTTL is how long an upstream answer is reused before the edge asks again.
	DefaultResolveTTL = time.Minute
	// DefaultGrace keeps a resolved address in the ledger a little past its TTL.
	DefaultGrace          = 30 * time.Second
	DefaultLedgerCapacity = 4096
	DefaultAuditLimit     = 200
	DefaultAuditWindow    = time.Second
)

// FilterConfig configures a Filter. Zero durations and sizes take the defaults above.
type FilterConfig struct {
	Clock    Clock
	Resolver Resolver
	// Observer receives every record. Nil drops them.
	Observer    AuditObserver
	FreezeStore FreezeStore
	AllowList   domain.AllowList
	// Edge is the edge's own address on the inner network: the only answer the machine's DNS
	// ever gets.
	Edge netip.Addr
	// Frozen starts the filter frozen.
	Frozen         bool
	AnswerTTL      time.Duration
	ResolveTTL     time.Duration
	Grace          time.Duration
	LedgerCapacity int
	AuditLimit     int
	AuditWindow    time.Duration
}

// Filter is the edge's internet wall. The DNS server asks it how to answer each query; the
// egress proxies ask it where, if anywhere, each request may go. It is safe for concurrent use
// and implements Freezer.
type Filter struct {
	clock      Clock
	resolver   Resolver
	observer   AuditObserver
	freezer    FreezeStore
	policy     domain.Policy
	edge       netip.Addr
	answerTTL  time.Duration
	resolveTTL time.Duration

	mu      sync.Mutex
	list    domain.AllowList
	ledger  *domain.Ledger
	limiter *domain.AuditLimiter
	frozen  bool
	open    map[*tracked]struct{}
}

var _ Freezer = (*Filter)(nil)

type tracked struct{ c io.Closer }

// NewFilter makes a filter.
func NewFilter(cfg FilterConfig) *Filter {
	return &Filter{
		clock:      cfg.Clock,
		resolver:   cfg.Resolver,
		observer:   cfg.Observer,
		freezer:    cfg.FreezeStore,
		policy:     domain.DefaultPolicy(),
		edge:       cfg.Edge,
		answerTTL:  orDefault(cfg.AnswerTTL, DefaultAnswerTTL),
		resolveTTL: orDefault(cfg.ResolveTTL, DefaultResolveTTL),
		list:       cfg.AllowList,
		ledger:     domain.NewLedger(orDefault(cfg.Grace, DefaultGrace), orDefault(cfg.LedgerCapacity, DefaultLedgerCapacity)),
		limiter:    domain.NewAuditLimiter(orDefault(cfg.AuditLimit, DefaultAuditLimit), orDefault(cfg.AuditWindow, DefaultAuditWindow)),
		frozen:     cfg.Frozen,
		open:       map[*tracked]struct{}{},
	}
}

func orDefault[T comparable](v, def T) T {
	var zero T
	if v == zero {
		return def
	}
	return v
}

// Query is one DNS question from the machine.
type Query struct {
	Source netip.AddrPort
	Name   string
	Type   domain.QueryType
}

// AnswerKind says how the DNS server answers. The zero AnswerKind is AnswerRefused.
type AnswerKind int

// Answer kinds.
const (
	// AnswerRefused: the name is not allowed. The server answers REFUSED.
	AnswerRefused AnswerKind = iota
	// AnswerAddresses: answer with Addrs (always the edge address).
	AnswerAddresses
	// AnswerNoData: the name is allowed but has no record of this type for the machine.
	AnswerNoData
)

// Answer is how to answer one Query.
type Answer struct {
	Kind  AnswerKind
	Addrs []netip.Addr
	TTL   time.Duration
}

// Resolve is the "answer a DNS query" use case. Allowed names (and the gateway name) answer the
// edge address; the upstream is never asked here. Every query is recorded.
func (f *Filter) Resolve(_ context.Context, q Query) Answer {
	name, err := domain.ParseName(q.Name)
	if err != nil {
		f.record(Event{Kind: "dns", Source: q.Source, Name: q.Name, Reason: string(domain.ReasonNotAName)})
		return Answer{Kind: AnswerRefused}
	}
	f.mu.Lock()
	action := f.policy.DecideQuery(name, q.Type, f.list)
	f.mu.Unlock()
	if action == domain.Refuse {
		f.record(Event{Kind: "dns", Source: q.Source, Name: name.String(), Reason: string(domain.ReasonNameNotAllowed)})
		return Answer{Kind: AnswerRefused}
	}
	f.record(Event{Kind: "dns", Source: q.Source, Name: name.String(), Allowed: true, Reason: string(domain.ReasonAllowedName)})
	if action == domain.AnswerEdge {
		return Answer{Kind: AnswerAddresses, Addrs: []netip.Addr{f.edge}, TTL: f.answerTTL}
	}
	return Answer{Kind: AnswerNoData, TTL: f.answerTTL}
}

// EgressRequest is the machine asking an egress proxy to reach Host on Port.
type EgressRequest struct {
	Source netip.AddrPort
	Host   string
	Port   uint16
	Via    domain.Via
}

// Route is where an allowed request may connect: only addresses the edge resolved for Name.
type Route struct {
	Name  domain.Name
	Addrs []netip.AddrPort
}

// Refusal is the error for a refused egress request.
type Refusal struct {
	Reason domain.Reason
}

func (r *Refusal) Error() string { return "refused: " + string(r.Reason) }

// Route is the "route an egress request" use case. It decides the request, resolves an allowed
// name upstream (reusing the ledger while it is fresh), keeps only public addresses, and returns
// them. Every request is recorded, allowed or refused.
func (f *Filter) Route(ctx context.Context, r EgressRequest) (Route, error) {
	f.mu.Lock()
	verdict := f.policy.DecideEgress(domain.EgressRequest{Host: r.Host, Port: r.Port, Via: r.Via}, f.list, f.frozen)
	f.mu.Unlock()
	name := r.Host
	if !verdict.Name.IsZero() {
		name = verdict.Name.String()
	}
	ev := Event{Kind: r.Via.String(), Source: r.Source, Name: name, Port: r.Port}
	if !verdict.Allowed {
		return Route{}, f.refuse(ev, verdict.Reason)
	}

	addrs, reason := f.addresses(ctx, verdict.Name)
	if reason != "" {
		return Route{}, f.refuse(ev, reason)
	}
	ev.Allowed, ev.Reason = true, string(domain.ReasonAllowedName)
	f.record(ev)
	route := Route{Name: verdict.Name}
	for _, a := range addrs {
		route.Addrs = append(route.Addrs, netip.AddrPortFrom(a, r.Port))
	}
	return route, nil
}

func (f *Filter) addresses(ctx context.Context, name domain.Name) ([]netip.Addr, domain.Reason) {
	f.mu.Lock()
	cached := f.ledger.Addrs(name, f.clock.Now())
	f.mu.Unlock()
	if len(cached) > 0 {
		return cached, ""
	}
	found, err := f.resolver.LookupIPv4(ctx, name.String())
	if err != nil {
		return nil, domain.ReasonNotResolved
	}
	public := domain.PublicAddresses(found)
	if len(public) == 0 {
		return nil, domain.ReasonForbiddenDestination
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ledger.Record(name, public, f.resolveTTL, f.clock.Now())
	return f.ledger.Addrs(name, f.clock.Now()), ""
}

func (f *Filter) refuse(ev Event, reason domain.Reason) error {
	ev.Reason = string(reason)
	f.record(ev)
	return &Refusal{Reason: reason}
}

// Track registers an open egress connection so Freeze can cut it. It returns false, and closes
// c, when egress is frozen. Call release when the connection ends.
func (f *Filter) Track(c io.Closer) (release func(), ok bool) {
	f.mu.Lock()
	if f.frozen {
		f.mu.Unlock()
		_ = c.Close()
		return func() {}, false
	}
	t := &tracked{c: c}
	f.open[t] = struct{}{}
	f.mu.Unlock()
	return func() {
		f.mu.Lock()
		delete(f.open, t)
		f.mu.Unlock()
	}, true
}

// Freeze is the "freeze all egress" use case: refuse every new request and cut every open
// connection at once. The switch is saved so a restarted edge comes back frozen. When saving
// fails the edge is still frozen and the error says the freeze may not survive a restart.
func (f *Filter) Freeze(ctx context.Context) error {
	f.mu.Lock()
	f.frozen = true
	open := f.open
	f.open = map[*tracked]struct{}{}
	f.mu.Unlock()
	for t := range open {
		_ = t.c.Close()
	}
	if err := f.freezer.Save(ctx, true); err != nil {
		return fmt.Errorf("egress is frozen, but the freeze was not saved and may not survive a restart: %w", err)
	}
	return nil
}

// Release lets egress through again. The release is saved first: if it cannot be saved, egress
// stays frozen, so a restart can never undo a freeze the human still sees as on.
func (f *Filter) Release(ctx context.Context) error {
	if err := f.freezer.Save(ctx, false); err != nil {
		return fmt.Errorf("egress stays frozen: %w", err)
	}
	f.mu.Lock()
	f.frozen = false
	f.mu.Unlock()
	return nil
}

// Frozen reports whether egress is frozen.
func (f *Filter) Frozen(context.Context) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.frozen, nil
}

// SetAllowList replaces the allow list. A removed name is refused from the next request on.
func (f *Filter) SetAllowList(l domain.AllowList) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.list = l
}

// AllowList returns the allow list in force.
func (f *Filter) AllowList() domain.AllowList {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.list
}

// record is the "record what happened" use case: stamp the time, apply the rate limit and hand
// the event to the observer.
func (f *Filter) record(e Event) {
	if f.observer == nil {
		return
	}
	e.At = f.clock.Now()
	f.mu.Lock()
	ok, suppressed := f.limiter.Allow(e.At)
	f.mu.Unlock()
	if !ok {
		return
	}
	e.Suppressed = suppressed
	f.observer.Record(e)
}

// IsRefusal reports whether err is a Refusal.
func IsRefusal(err error) bool {
	var r *Refusal
	return errors.As(err, &r)
}
