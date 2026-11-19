// SPDX-License-Identifier: MIT

package domain_test

import (
	"net/netip"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/SDK-E/agentsone/internal/edge/net/domain"
)

func TestPolicy_DecideQuery(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		host  string
		qtype domain.QueryType
		want  domain.QueryAction
	}{
		{"gateway name answers the edge", "gateway.agentsone.internal", domain.QueryA, domain.AnswerEdge},
		{"gateway name ignores case", "Gateway.AgentsOne.Internal", domain.QueryA, domain.AnswerEdge},
		{"gateway name has no ipv6 address", "gateway.agentsone.internal", domain.QueryAAAA, domain.AnswerEmpty},
		{"allowed name answers the edge", "github.com", domain.QueryA, domain.AnswerEdge},
		{"allowed name has no ipv6 address", "github.com", domain.QueryAAAA, domain.AnswerEmpty},
		{"allowed name gets no other record types", "github.com", domain.QueryOther, domain.AnswerEmpty},
		{"name not on the list is refused", "example.com", domain.QueryA, domain.Refuse},
		{"name not on the list is refused for every type", "example.com", domain.QueryOther, domain.Refuse},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := domain.DefaultPolicy().DecideQuery(mustName(t, tt.host), tt.qtype, mustList(t, "github.com"))
			if got != tt.want {
				t.Fatalf("DecideQuery(%q) = %v, want %v", tt.host, got, tt.want)
			}
		})
	}
}

func TestPolicy_DecideEgress(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		req     domain.EgressRequest
		frozen  bool
		allowed bool
		reason  domain.Reason
	}{
		{"https to an allowed name", domain.EgressRequest{Host: "github.com", Port: 443, Via: domain.ViaTLS}, false, true, domain.ReasonAllowedName},
		{"http to an allowed name", domain.EgressRequest{Host: "GitHub.com.", Port: 80, Via: domain.ViaHTTP}, false, true, domain.ReasonAllowedName},
		{"connect to an allowed name on 443", domain.EgressRequest{Host: "github.com", Port: 443, Via: domain.ViaConnect}, false, true, domain.ReasonAllowedName},
		{"connect to an allowed name on 80", domain.EgressRequest{Host: "github.com", Port: 80, Via: domain.ViaConnect}, false, true, domain.ReasonAllowedName},
		{"connect to an allowed name on 22", domain.EgressRequest{Host: "github.com", Port: 22, Via: domain.ViaConnect}, false, false, domain.ReasonPortNotAllowed},
		{"tls on a port other than 443", domain.EgressRequest{Host: "github.com", Port: 8443, Via: domain.ViaTLS}, false, false, domain.ReasonPortNotAllowed},
		{"http on a port other than 80", domain.EgressRequest{Host: "github.com", Port: 8080, Via: domain.ViaHTTP}, false, false, domain.ReasonPortNotAllowed},
		{"name not on the list", domain.EgressRequest{Host: "example.com", Port: 443, Via: domain.ViaTLS}, false, false, domain.ReasonNameNotAllowed},
		{"ip address instead of a name", domain.EgressRequest{Host: "140.82.121.4", Port: 443, Via: domain.ViaConnect}, false, false, domain.ReasonNotAName},
		{"no name at all", domain.EgressRequest{Host: "", Port: 443, Via: domain.ViaTLS}, false, false, domain.ReasonNotAName},
		{"malformed name", domain.EgressRequest{Host: "git hub.com", Port: 443, Via: domain.ViaTLS}, false, false, domain.ReasonNotAName},
		{"the gateway name is not egress", domain.EgressRequest{Host: domain.GatewayName, Port: 443, Via: domain.ViaConnect}, false, false, domain.ReasonInternalName},
		{"frozen blocks an allowed name", domain.EgressRequest{Host: "github.com", Port: 443, Via: domain.ViaTLS}, true, false, domain.ReasonFrozen},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := domain.DefaultPolicy().DecideEgress(tt.req, mustList(t, "github.com"), tt.frozen)
			if got.Allowed != tt.allowed || got.Reason != tt.reason {
				t.Fatalf("DecideEgress() = %v/%q, want %v/%q", got.Allowed, got.Reason, tt.allowed, tt.reason)
			}
		})
	}
}

func TestPolicy_DecideEgressNamesTheNormalisedName(t *testing.T) {
	t.Parallel()
	got := domain.DefaultPolicy().DecideEgress(domain.EgressRequest{Host: "GitHub.com.", Port: 443, Via: domain.ViaTLS}, mustList(t, "github.com"), false)
	if got.Name.String() != "github.com" {
		t.Fatalf("Name = %q, want github.com", got.Name)
	}
}

func TestIsPublicDestination(t *testing.T) {
	t.Parallel()
	tests := []struct {
		addr string
		want bool
	}{
		{"140.82.121.4", true},
		{"203.0.113.10", true},
		{"::ffff:140.82.121.4", true},
		{"127.0.0.1", false},
		{"0.0.0.0", false},
		{"0.1.2.3", false},
		{"10.1.2.3", false},
		{"172.16.0.1", false},
		{"192.168.1.1", false},
		{"169.254.169.254", false},
		{"100.64.0.1", false},
		{"100.127.255.254", false},
		{"100.128.0.1", true},
		{"198.18.0.1", false},
		{"224.0.0.1", false},
		{"240.0.0.1", false},
		{"255.255.255.255", false},
		{"2606:4700::1111", false},
	}
	for _, tt := range tests {
		t.Run(tt.addr, func(t *testing.T) {
			t.Parallel()
			if got := domain.IsPublicDestination(netip.MustParseAddr(tt.addr)); got != tt.want {
				t.Fatalf("IsPublicDestination(%s) = %v, want %v", tt.addr, got, tt.want)
			}
		})
	}
}

func TestPublicAddresses_KeepsOnlyPublicIPv4WithoutDuplicates(t *testing.T) {
	t.Parallel()
	in := []netip.Addr{
		netip.MustParseAddr("140.82.121.4"),
		netip.MustParseAddr("127.0.0.1"),
		netip.MustParseAddr("2606:4700::1111"),
		netip.MustParseAddr("::ffff:140.82.121.4"),
		netip.MustParseAddr("140.82.121.3"),
	}

	got := domain.PublicAddresses(in)

	want := []netip.Addr{netip.MustParseAddr("140.82.121.4"), netip.MustParseAddr("140.82.121.3")}
	if diff := cmp.Diff(want, got, cmp.Comparer(func(a, b netip.Addr) bool { return a == b })); diff != "" {
		t.Fatalf("PublicAddresses() mismatch (-want +got):\n%s", diff)
	}
}

func TestAuditLimiter_AllowsUpToTheLimitInAWindow(t *testing.T) {
	t.Parallel()
	l := domain.NewAuditLimiter(2, time.Second)
	got := []bool{ok(l.Allow(t0)), ok(l.Allow(t0)), ok(l.Allow(t0))}
	if diff := cmp.Diff([]bool{true, true, false}, got); diff != "" {
		t.Fatalf("Allow() mismatch (-want +got):\n%s", diff)
	}
}

func TestAuditLimiter_AllowsAgainAfterTheWindow(t *testing.T) {
	t.Parallel()
	l := domain.NewAuditLimiter(1, time.Second)
	l.Allow(t0)
	if ok(l.Allow(t0.Add(time.Second - time.Nanosecond))) {
		t.Fatal("second record inside the window was allowed")
	}
	if !ok(l.Allow(t0.Add(time.Second))) {
		t.Fatal("record at the window edge was refused")
	}
}

func TestAuditLimiter_ReportsSuppressedCountOnNextAllowedRecord(t *testing.T) {
	t.Parallel()
	l := domain.NewAuditLimiter(1, time.Second)
	l.Allow(t0)
	l.Allow(t0)
	l.Allow(t0)

	allowed, suppressed := l.Allow(t0.Add(time.Second))

	if !allowed || suppressed != 2 {
		t.Fatalf("Allow() = %v, %d, want true, 2", allowed, suppressed)
	}
}

func ok(allowed bool, _ int) bool { return allowed }
