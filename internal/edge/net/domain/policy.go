// SPDX-License-Identifier: MIT

package domain

import (
	"net/netip"
	"slices"
	"time"
)

// Reason explains a decision in words a human can read in the audit log.
type Reason string

// Reasons.
const (
	ReasonAllowedName          Reason = "name is on the allow list"
	ReasonNameNotAllowed       Reason = "name is not on the allow list"
	ReasonNotAName             Reason = "no valid host name was given (IP addresses are refused)"
	ReasonInternalName         Reason = "internal name; reach edge services directly, not through the proxy"
	ReasonPortNotAllowed       Reason = "port is not allowed for this entry point"
	ReasonFrozen               Reason = "all egress is frozen"
	ReasonForbiddenDestination Reason = "name resolved only to addresses that are not public internet addresses"
	ReasonNotResolved          Reason = "name could not be resolved"
)

// QueryType is the DNS record type asked for, reduced to what the policy needs.
type QueryType int

// Query types.
const (
	QueryA QueryType = iota + 1
	QueryAAAA
	QueryOther
)

// QueryAction is what the DNS server does with a query. The zero QueryAction is Refuse, so a
// forgotten case fails closed.
type QueryAction int

// Query actions.
const (
	// Refuse answers REFUSED and records the attempt.
	Refuse QueryAction = iota
	// AnswerEdge answers with the edge's own inner address: the machine then connects to the
	// edge proxies, which decide again and connect on its behalf.
	AnswerEdge
	// AnswerEmpty answers that the name exists but has no record of this type.
	AnswerEmpty
)

// GatewayName always resolves to the edge, whatever the allow list says.
const GatewayName = "gateway.agentsone.internal"

// Via is the edge entry point a connection came in through.
type Via int

// Entry points.
const (
	// ViaHTTP is the transparent HTTP proxy (port 80), routed by the Host header.
	ViaHTTP Via = iota + 1
	// ViaTLS is the transparent TLS proxy (port 443), routed by the TLS server name.
	ViaTLS
	// ViaConnect is the proxy for proxy-aware tools (port 3128): CONNECT and plain HTTP.
	ViaConnect
)

func (v Via) String() string {
	switch v {
	case ViaHTTP:
		return "http"
	case ViaTLS:
		return "tls"
	case ViaConnect:
		return "proxy"
	default:
		return "unknown"
	}
}

// EgressRequest is the machine asking to reach Host on Port through an entry point. Host is
// what the machine sent (Host header, TLS server name or proxy target), not yet trusted.
type EgressRequest struct {
	Host string
	Port uint16
	Via  Via
}

// EgressVerdict is the decision for an EgressRequest. Name is the normalised name when Host
// parsed as one.
type EgressVerdict struct {
	Allowed bool
	Reason  Reason
	Name    Name
}

// Policy holds the fixed shape of egress: which ports each entry point may reach.
type Policy struct {
	ports       map[Via][]uint16
	gatewayName Name
}

// DefaultPolicy is the edge policy: the transparent proxies reach port 80 and 443 only, and the
// proxy for proxy-aware tools reaches both.
func DefaultPolicy() Policy {
	name, err := ParseName(GatewayName)
	if err != nil {
		panic(err)
	}
	return Policy{
		ports: map[Via][]uint16{
			ViaHTTP:    {80},
			ViaTLS:     {443},
			ViaConnect: {80, 443},
		},
		gatewayName: name,
	}
}

// DecideQuery returns what the DNS server does with a query for n. Allowed names answer the
// edge, never the real address: the machine has no route to the internet, only to the edge.
func (p Policy) DecideQuery(n Name, t QueryType, list AllowList) QueryAction {
	if n != p.gatewayName && !list.Allows(n) {
		return Refuse
	}
	if t == QueryA {
		return AnswerEdge
	}
	return AnswerEmpty
}

// DecideEgress returns the verdict for an egress request. Frozen refuses everything.
func (p Policy) DecideEgress(r EgressRequest, list AllowList, frozen bool) EgressVerdict {
	if frozen {
		return EgressVerdict{Reason: ReasonFrozen}
	}
	name, err := ParseName(r.Host)
	if err != nil {
		return EgressVerdict{Reason: ReasonNotAName}
	}
	switch {
	case name == p.gatewayName:
		return EgressVerdict{Reason: ReasonInternalName, Name: name}
	case !list.Allows(name):
		return EgressVerdict{Reason: ReasonNameNotAllowed, Name: name}
	case !slices.Contains(p.ports[r.Via], r.Port):
		return EgressVerdict{Reason: ReasonPortNotAllowed, Name: name}
	}
	return EgressVerdict{Allowed: true, Reason: ReasonAllowedName, Name: name}
}

// Ranges that are not public internet destinations even though Go counts them as global
// unicast. Answers in these ranges would let an allowed name point the edge at itself, the
// machine's network, the host's network or a carrier-grade NAT (for example a VPN).
var nonPublic = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("240.0.0.0/4"),
}

// IsPublicDestination reports whether addr is a public IPv4 unicast address. Loopback, private,
// link-local, carrier-grade NAT, multicast, reserved and IPv6 addresses are not.
func IsPublicDestination(addr netip.Addr) bool {
	addr = addr.Unmap()
	if !addr.Is4() || !addr.IsGlobalUnicast() || addr.IsPrivate() {
		return false
	}
	for _, p := range nonPublic {
		if p.Contains(addr) {
			return false
		}
	}
	return true
}

// PublicAddresses keeps the public IPv4 addresses of addrs, unmapped, in order and without
// duplicates.
func PublicAddresses(addrs []netip.Addr) []netip.Addr {
	var out []netip.Addr
	for _, a := range addrs {
		a = a.Unmap()
		if IsPublicDestination(a) && !slices.Contains(out, a) {
			out = append(out, a)
		}
	}
	return out
}

// AuditLimiter caps how many records are written per window, so a machine that hammers the
// wall cannot flood the audit sink. Records over the cap are counted, and the count rides on the
// next record that is let through.
type AuditLimiter struct {
	limit       int
	window      time.Duration
	windowStart time.Time
	count       int
	suppressed  int
}

// NewAuditLimiter allows limit records per window.
func NewAuditLimiter(limit int, window time.Duration) *AuditLimiter {
	return &AuditLimiter{limit: limit, window: window}
}

// Allow reports whether a record at now may be written and, if so, how many were suppressed
// since the last one written.
func (l *AuditLimiter) Allow(now time.Time) (bool, int) {
	if l.windowStart.IsZero() || !now.Before(l.windowStart.Add(l.window)) {
		l.windowStart = now
		l.count = 0
	}
	if l.count >= l.limit {
		l.suppressed++
		return false, 0
	}
	l.count++
	suppressed := l.suppressed
	l.suppressed = 0
	return true, suppressed
}
