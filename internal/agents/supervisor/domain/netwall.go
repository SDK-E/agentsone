// SPDX-License-Identifier: MIT

package domain

import (
	"maps"
	"slices"
	"strings"
)

// NetWallFix is what the operator does when the machine has a way out
// besides the edge: compose.yaml puts the machine on the inner network only,
// and Coolify's normal mode adds a network with a gateway to every service.
const NetWallFix = "Deploy with Raw compose mode and do not attach other networks to the machine service"

// netWallUnreadableFix is what the operator does when the routing table
// cannot be read: only a Linux container, as compose.yaml runs it, has one.
const netWallUnreadableFix = "Run the machine as compose.yaml does, in a Linux container"

// NetWall is the machine's network wall as the daemon sees it from inside.
// It holds when the machine has no route out and sits on one network only,
// the inner one, so its only way out is the edge.
type NetWall struct {
	// Holds is true when the machine has no way out but the edge.
	Holds bool
	// Interfaces are the machine's networks: its interfaces besides
	// loopback, sorted.
	Interfaces []string
	// RoutesOut are the routes that lead beyond the machine's own network,
	// as "<destination> via <gateway> dev <interface>".
	RoutesOut []string
	// Reason says in plain words why the wall does not hold, and Fix what
	// the operator does about it. Both are empty when it holds.
	Reason string
	Fix    string
	// Err is why the routing table could not be read, for the log only.
	Err error
}

// StateReason is the sentence shown under every agent while the wall does
// not hold; empty when it holds.
func (w NetWall) StateReason() string {
	if w.Holds {
		return ""
	}
	return "The network wall does not hold, so no agent may wake: " + w.Reason + ". " + w.Fix + "."
}

// Same reports whether w and o say the same thing, so a recheck logs only
// a change.
func (w NetWall) Same(o NetWall) bool {
	return w.Holds == o.Holds && w.Reason == o.Reason &&
		slices.Equal(w.Interfaces, o.Interfaces) && slices.Equal(w.RoutesOut, o.RoutesOut)
}

// Route is one row of the machine's routing table, IPv4 or IPv6. Rows on
// loopback and reject rows (unreachable, prohibit) lead nowhere and are
// left out by the reader.
type Route struct {
	Iface string
	// Out is a default route or a route through a gateway: a way to
	// addresses beyond the machine's own network.
	Out bool
	// Desc names the route for the log.
	Desc string
}

// fallbackTunnels are the devices the kernel adds to every network
// namespace when a tunnel module is loaded on the host. They start down,
// with no address and no route, so they are no network unless a route uses
// them.
var fallbackTunnels = []string{"tunl0", "sit0", "ip6tnl0", "gre0", "gretap0", "erspan0", "ip6gre0", "ip_vti0", "ip6_vti0"}

// JudgeNetWall decides whether the wall holds for the machine's interfaces
// (loopback included or not) and its routes. A route out breaks it; so
// does a second network, since only the inner one has no way out.
func JudgeNetWall(ifaces []string, routes []Route) NetWall {
	routed := map[string]bool{}
	var out, outIfaces []string
	for _, r := range routes {
		routed[r.Iface] = true
		if r.Out {
			out = append(out, r.Desc)
			if !slices.Contains(outIfaces, r.Iface) {
				outIfaces = append(outIfaces, r.Iface)
			}
		}
	}
	nets := map[string]bool{}
	for _, i := range ifaces {
		if i != "lo" && (routed[i] || !slices.Contains(fallbackTunnels, i)) {
			nets[i] = true
		}
	}
	for i := range routed {
		nets[i] = true
	}
	w := NetWall{Interfaces: slices.Sorted(maps.Keys(nets)), RoutesOut: out}
	switch {
	case len(out) > 0:
		slices.Sort(outIfaces)
		w.Reason = "the machine has a direct route to the internet (through " + strings.Join(outIfaces, ", ") + ")"
		w.Fix = NetWallFix
	case len(w.Interfaces) > 1:
		w.Reason = "the machine is on more than one network (" + strings.Join(w.Interfaces, ", ") + ")"
		w.Fix = NetWallFix
	default:
		w.Holds = true
	}
	return w
}

// NetWallUnproven is the wall when the routing table cannot be read: it
// does not count as holding.
func NetWallUnproven(err error) NetWall {
	return NetWall{
		Reason: "the machine's routing table cannot be read, so the wall cannot be proven",
		Fix:    netWallUnreadableFix,
		Err:    err,
	}
}
