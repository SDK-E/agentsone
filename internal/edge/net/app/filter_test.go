// SPDX-License-Identifier: MIT

package app_test

import (
	"errors"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/SDK-E/agentsone/internal/edge/net/app"
	"github.com/SDK-E/agentsone/internal/edge/net/domain"
	"github.com/SDK-E/agentsone/internal/edge/net/nettest"
)

var (
	t0   = time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	vm   = netip.MustParseAddrPort("172.30.0.5:40000")
	edge = netip.MustParseAddr("172.30.0.2")

	netipComparers = cmp.Options{
		cmp.Comparer(func(a, b netip.Addr) bool { return a == b }),
		cmp.Comparer(func(a, b netip.AddrPort) bool { return a == b }),
	}
)

type fixture struct {
	clock    *nettest.Clock
	resolver *nettest.Resolver
	observer *nettest.Observer
	freeze   *nettest.FreezeStore
	filter   *app.Filter
}

func newFixture(t *testing.T, allowed ...string) fixture {
	t.Helper()
	f := fixture{clock: nettest.NewClock(t0), resolver: nettest.NewResolver(), observer: nettest.NewObserver(), freeze: nettest.NewFreezeStore()}
	f.filter = app.NewFilter(app.FilterConfig{
		Clock:       f.clock,
		Resolver:    f.resolver,
		Observer:    f.observer,
		FreezeStore: f.freeze,
		AllowList:   list(t, allowed...),
		Edge:        edge,
		AuditLimit:  100,
	})
	return f
}

func list(t *testing.T, patterns ...string) domain.AllowList {
	t.Helper()
	entries := make([]domain.Entry, 0, len(patterns))
	for _, p := range patterns {
		pat, err := domain.ParsePattern(p)
		if err != nil {
			t.Fatalf("ParsePattern(%q) error = %v", p, err)
		}
		entries = append(entries, domain.Entry{Pattern: pat, Why: "test"})
	}
	l, err := domain.NewAllowList(entries)
	if err != nil {
		t.Fatalf("NewAllowList() error = %v", err)
	}
	return l
}

func tlsTo(host string) app.EgressRequest {
	return app.EgressRequest{Source: vm, Host: host, Port: 443, Via: domain.ViaTLS}
}

func TestResolve_AnswersAllowedNameWithTheEdgeAddress(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "github.com")
	f.resolver.FailWith(errors.New("DNS answers must not ask upstream"))

	got := f.filter.Resolve(t.Context(), app.Query{Source: vm, Name: "GitHub.com.", Type: domain.QueryA})

	want := app.Answer{Kind: app.AnswerAddresses, Addrs: []netip.Addr{edge}, TTL: app.DefaultAnswerTTL}
	if diff := cmp.Diff(want, got, netipComparers); diff != "" {
		t.Fatalf("Resolve() mismatch (-want +got):\n%s", diff)
	}
}

func TestResolve_AnswersTheGatewayNameWithTheEdgeAddress(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	got := f.filter.Resolve(t.Context(), app.Query{Source: vm, Name: domain.GatewayName, Type: domain.QueryA})

	if got.Kind != app.AnswerAddresses || len(got.Addrs) != 1 || got.Addrs[0] != edge {
		t.Fatalf("Resolve() = %+v, want the edge address", got)
	}
}

func TestResolve_AnswersNoDataForAllowedNameIPv6(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "github.com")

	got := f.filter.Resolve(t.Context(), app.Query{Source: vm, Name: "github.com", Type: domain.QueryAAAA})

	if got.Kind != app.AnswerNoData {
		t.Fatalf("Resolve() kind = %v, want no data", got.Kind)
	}
}

func TestResolve_RefusesNameNotOnTheList(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "github.com")

	got := f.filter.Resolve(t.Context(), app.Query{Source: vm, Name: "example.com", Type: domain.QueryA})

	if got.Kind != app.AnswerRefused {
		t.Fatalf("Resolve() kind = %v, want refused", got.Kind)
	}
}

func TestResolve_RefusesMalformedName(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "github.com")

	got := f.filter.Resolve(t.Context(), app.Query{Source: vm, Name: "bad name", Type: domain.QueryA})

	if got.Kind != app.AnswerRefused {
		t.Fatalf("Resolve() kind = %v, want refused", got.Kind)
	}
}

func TestResolve_RecordsEveryQuery(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "github.com")

	f.filter.Resolve(t.Context(), app.Query{Source: vm, Name: "github.com", Type: domain.QueryA})
	f.filter.Resolve(t.Context(), app.Query{Source: vm, Name: "Example.com", Type: domain.QueryA})

	want := []app.Event{
		{At: t0, Kind: "dns", Source: vm, Name: "github.com", Allowed: true, Reason: string(domain.ReasonAllowedName)},
		{At: t0, Kind: "dns", Source: vm, Name: "example.com", Reason: string(domain.ReasonNameNotAllowed)},
	}
	if diff := cmp.Diff(want, f.observer.Events(), netipComparers); diff != "" {
		t.Fatalf("events mismatch (-want +got):\n%s", diff)
	}
}

func TestRoute_ReturnsTheResolvedAddressesOfAnAllowedName(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "github.com")
	f.resolver.Set("github.com", "140.82.121.4", "140.82.121.3")

	got, err := f.filter.Route(t.Context(), tlsTo("GitHub.com"))
	if err != nil {
		t.Fatalf("Route() error = %v", err)
	}

	want := []netip.AddrPort{netip.MustParseAddrPort("140.82.121.3:443"), netip.MustParseAddrPort("140.82.121.4:443")}
	if diff := cmp.Diff(want, got.Addrs, netipComparers); diff != "" || got.Name.String() != "github.com" {
		t.Fatalf("Route() = %v %v, want github.com with (-want +got):\n%s", got.Name, got.Addrs, diff)
	}
}

func TestRoute_RecordsTheAllowedRequest(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "github.com")
	f.resolver.Set("github.com", "140.82.121.4")

	if _, err := f.filter.Route(t.Context(), tlsTo("github.com")); err != nil {
		t.Fatalf("Route() error = %v", err)
	}

	want := []app.Event{{At: t0, Kind: "tls", Source: vm, Name: "github.com", Port: 443, Allowed: true, Reason: string(domain.ReasonAllowedName)}}
	if diff := cmp.Diff(want, f.observer.Events(), netipComparers); diff != "" {
		t.Fatalf("events mismatch (-want +got):\n%s", diff)
	}
}

func TestRoute_RefusesAndRecordsANameNotOnTheList(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "github.com")
	f.resolver.Set("example.com", "93.184.215.14")

	_, err := f.filter.Route(t.Context(), tlsTo("example.com"))

	var refusal *app.Refusal
	if !errors.As(err, &refusal) || refusal.Reason != domain.ReasonNameNotAllowed {
		t.Fatalf("Route() error = %v, want a refusal for a name not on the list", err)
	}
	events := f.observer.Events()
	if len(events) != 1 || events[0].Allowed || events[0].Name != "example.com" {
		t.Fatalf("events = %+v, want one refused record for example.com", events)
	}
}

func TestRoute_NeverAsksUpstreamForARefusedName(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "github.com")

	_, _ = f.filter.Route(t.Context(), tlsTo("example.com"))

	if got := f.resolver.Asked(); len(got) != 0 {
		t.Fatalf("upstream asked for %v, want nothing", got)
	}
}

func TestRoute_ConnectsOnlyToPublicAddresses(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "github.com")
	f.resolver.Set("github.com", "127.0.0.1", "140.82.121.4", "172.30.0.2")

	got, err := f.filter.Route(t.Context(), tlsTo("github.com"))
	if err != nil {
		t.Fatalf("Route() error = %v", err)
	}

	if diff := cmp.Diff([]netip.AddrPort{netip.MustParseAddrPort("140.82.121.4:443")}, got.Addrs, netipComparers); diff != "" {
		t.Fatalf("addresses mismatch (-want +got):\n%s", diff)
	}
}

func TestRoute_RefusesANameThatPointsOnlyInside(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "github.com")
	f.resolver.Set("github.com", "127.0.0.1", "10.0.0.1")

	_, err := f.filter.Route(t.Context(), tlsTo("github.com"))

	var refusal *app.Refusal
	if !errors.As(err, &refusal) || refusal.Reason != domain.ReasonForbiddenDestination {
		t.Fatalf("Route() error = %v, want a forbidden destination refusal", err)
	}
}

func TestRoute_RefusesANameThatDoesNotResolve(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "*.github.com")

	_, err := f.filter.Route(t.Context(), tlsTo("missing.github.com"))

	var refusal *app.Refusal
	if !errors.As(err, &refusal) || refusal.Reason != domain.ReasonNotResolved {
		t.Fatalf("Route() error = %v, want a not resolved refusal", err)
	}
}

func TestRoute_ReusesTheLedgerUntilItExpires(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "github.com")
	f.resolver.Set("github.com", "140.82.121.4")
	if _, err := f.filter.Route(t.Context(), tlsTo("github.com")); err != nil {
		t.Fatalf("Route() error = %v", err)
	}
	f.resolver.Set("github.com", "140.82.121.9")

	cached, _ := f.filter.Route(t.Context(), tlsTo("github.com"))
	f.clock.Advance(app.DefaultResolveTTL + app.DefaultGrace)
	fresh, _ := f.filter.Route(t.Context(), tlsTo("github.com"))

	got := []string{cached.Addrs[0].String(), fresh.Addrs[0].String()}
	if diff := cmp.Diff([]string{"140.82.121.4:443", "140.82.121.9:443"}, got); diff != "" {
		t.Fatalf("addresses mismatch (-want +got):\n%s", diff)
	}
}

func TestRoute_KeepsAddressesOfOneNameApartFromAnother(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "github.com", "deb.debian.org")
	f.resolver.Set("github.com", "140.82.121.4")
	f.resolver.Set("deb.debian.org", "151.101.2.132")
	if _, err := f.filter.Route(t.Context(), tlsTo("github.com")); err != nil {
		t.Fatalf("Route() error = %v", err)
	}

	got, err := f.filter.Route(t.Context(), tlsTo("deb.debian.org"))
	if err != nil {
		t.Fatalf("Route() error = %v", err)
	}

	if diff := cmp.Diff([]netip.AddrPort{netip.MustParseAddrPort("151.101.2.132:443")}, got.Addrs, netipComparers); diff != "" {
		t.Fatalf("addresses mismatch (-want +got):\n%s", diff)
	}
}

func TestRoute_ReportsUpstreamFailureAsARefusal(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "github.com")
	f.resolver.FailWith(errors.New("network down"))

	_, err := f.filter.Route(t.Context(), tlsTo("github.com"))

	var refusal *app.Refusal
	if !errors.As(err, &refusal) || refusal.Reason != domain.ReasonNotResolved {
		t.Fatalf("Route() error = %v, want a not resolved refusal", err)
	}
}

func TestRoute_RateLimitsRecords(t *testing.T) {
	t.Parallel()
	clock := nettest.NewClock(t0)
	observer := nettest.NewObserver()
	filter := app.NewFilter(app.FilterConfig{
		Clock: clock, Resolver: nettest.NewResolver(), Observer: observer, FreezeStore: nettest.NewFreezeStore(),
		Edge: edge, AuditLimit: 1, AuditWindow: time.Second,
	})

	for range 3 {
		_, _ = filter.Route(t.Context(), tlsTo("example.com"))
	}
	clock.Advance(time.Second)
	_, _ = filter.Route(t.Context(), tlsTo("example.com"))

	got := []int{}
	for _, e := range observer.Events() {
		got = append(got, e.Suppressed)
	}
	if diff := cmp.Diff([]int{0, 2}, got); diff != "" {
		t.Fatalf("suppressed counts mismatch (-want +got):\n%s", diff)
	}
}

func TestSetAllowList_RefusesARemovedName(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "github.com")
	f.resolver.Set("github.com", "140.82.121.4")
	if _, err := f.filter.Route(t.Context(), tlsTo("github.com")); err != nil {
		t.Fatalf("Route() error = %v", err)
	}

	f.filter.SetAllowList(list(t, "deb.debian.org"))

	if _, err := f.filter.Route(t.Context(), tlsTo("github.com")); err == nil {
		t.Fatal("Route() to a removed name succeeded")
	}
}

func TestFreeze_RefusesEveryEgressRequest(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "github.com")
	f.resolver.Set("github.com", "140.82.121.4")

	if err := f.filter.Freeze(t.Context()); err != nil {
		t.Fatalf("Freeze() error = %v", err)
	}

	_, err := f.filter.Route(t.Context(), tlsTo("github.com"))
	var refusal *app.Refusal
	if !errors.As(err, &refusal) || refusal.Reason != domain.ReasonFrozen {
		t.Fatalf("Route() error = %v, want a frozen refusal", err)
	}
}

func TestFreeze_CutsEveryOpenConnection(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "github.com")
	var a, b closeCounter
	_, okA := f.filter.Track(&a)
	release, okB := f.filter.Track(&b)
	release()

	if err := f.filter.Freeze(t.Context()); err != nil {
		t.Fatalf("Freeze() error = %v", err)
	}

	if !okA || !okB || a.n.Load() != 1 || b.n.Load() != 0 {
		t.Fatalf("closed = %d, %d; want only the still-open connection closed", a.n.Load(), b.n.Load())
	}
}

func TestFreeze_RefusesToTrackNewConnections(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	if err := f.filter.Freeze(t.Context()); err != nil {
		t.Fatalf("Freeze() error = %v", err)
	}
	var c closeCounter

	_, ok := f.filter.Track(&c)

	if ok || c.n.Load() != 1 {
		t.Fatalf("Track() ok = %v closed %d; want refused and closed", ok, c.n.Load())
	}
}

func TestFreeze_IsSaved(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	if err := f.filter.Freeze(t.Context()); err != nil {
		t.Fatalf("Freeze() error = %v", err)
	}

	if saved, _ := f.freeze.Load(t.Context()); !saved {
		t.Fatal("freeze was not saved")
	}
}

func TestFreeze_StaysOnWhenItCannotBeSaved(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "github.com")
	f.freeze.FailSaveWith(errors.New("disk full"))

	err := f.filter.Freeze(t.Context())

	frozen, _ := f.filter.Frozen(t.Context())
	if err == nil || !frozen {
		t.Fatalf("Freeze() error = %v, frozen = %v; want the error and still frozen", err, frozen)
	}
}

func TestRelease_LetsEgressThroughAgain(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "github.com")
	f.resolver.Set("github.com", "140.82.121.4")
	if err := f.filter.Freeze(t.Context()); err != nil {
		t.Fatalf("Freeze() error = %v", err)
	}

	if err := f.filter.Release(t.Context()); err != nil {
		t.Fatalf("Release() error = %v", err)
	}

	if _, err := f.filter.Route(t.Context(), tlsTo("github.com")); err != nil {
		t.Fatalf("Route() after release error = %v", err)
	}
	if saved, _ := f.freeze.Load(t.Context()); saved {
		t.Fatal("release was not saved")
	}
}

func TestRelease_StaysFrozenWhenItCannotBeSaved(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	if err := f.filter.Freeze(t.Context()); err != nil {
		t.Fatalf("Freeze() error = %v", err)
	}
	f.freeze.FailSaveWith(errors.New("disk full"))

	err := f.filter.Release(t.Context())

	frozen, _ := f.filter.Frozen(t.Context())
	if err == nil || !frozen {
		t.Fatalf("Release() error = %v, frozen = %v; want the error and still frozen", err, frozen)
	}
}

func TestNewFilter_StartsFrozenWhenConfiguredSo(t *testing.T) {
	t.Parallel()
	filter := app.NewFilter(app.FilterConfig{
		Clock: nettest.NewClock(t0), Resolver: nettest.NewResolver(), FreezeStore: nettest.NewFreezeStore(), Edge: edge, Frozen: true,
	})

	if frozen, _ := filter.Frozen(t.Context()); !frozen {
		t.Fatal("filter did not start frozen")
	}
}

func TestNewFilter_WithoutObserverStillRefuses(t *testing.T) {
	t.Parallel()
	filter := app.NewFilter(app.FilterConfig{Clock: nettest.NewClock(t0), Resolver: nettest.NewResolver(), FreezeStore: nettest.NewFreezeStore(), Edge: edge})

	if _, err := filter.Route(t.Context(), tlsTo("github.com")); err == nil {
		t.Fatal("Route() succeeded with an empty list")
	}
}

type closeCounter struct{ n atomic.Int32 }

func (c *closeCounter) Close() error { c.n.Add(1); return nil }
