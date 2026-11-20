// SPDX-License-Identifier: MIT

package domain

import (
	"fmt"
	"net/netip"
	"slices"
	"strings"
)

// DenyList is a set of destination ranges the operator denies for egress, on top of the ranges
// the edge always refuses (see IsPublicDestination). It is for public addresses the machine must
// never reach even through an allowed name, for example the host's own public addresses, which
// a proxy on the same host may trust. The zero DenyList denies nothing.
type DenyList struct {
	ranges []netip.Prefix
}

// NewDenyList makes a deny list of ranges. Host bits are cleared and IPv4-mapped IPv6 ranges are
// read as IPv4, so every range denies at least what it says.
func NewDenyList(ranges []netip.Prefix) DenyList {
	out := make([]netip.Prefix, 0, len(ranges))
	for _, p := range ranges {
		out = append(out, normalise(p))
	}
	return DenyList{ranges: out}
}

// ParseDenyRanges reads a comma-separated list of CIDR ranges or single addresses, IPv4 or IPv6,
// for example "203.0.113.7/32,2001:db8::/48". A single address is one host. Spaces around items
// and empty items are ignored, so an empty text denies nothing. Anything else is an error that
// names the item.
func ParseDenyRanges(text string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for item := range strings.SplitSeq(text, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		p, err := parseRange(item)
		if err != nil {
			return nil, fmt.Errorf("deny range %q is not an IP address or CIDR range (for example 203.0.113.7/32 or 2001:db8::/48)", item)
		}
		out = append(out, normalise(p))
	}
	return out, nil
}

func parseRange(item string) (netip.Prefix, error) {
	if strings.Contains(item, "/") {
		return netip.ParsePrefix(item)
	}
	addr, err := netip.ParseAddr(item)
	if err != nil {
		return netip.Prefix{}, err
	}
	if addr.Zone() != "" {
		return netip.Prefix{}, fmt.Errorf("address %s has a zone", addr)
	}
	return netip.PrefixFrom(addr, addr.BitLen()), nil
}

func normalise(p netip.Prefix) netip.Prefix {
	if p.Addr().Is4In6() && p.Bits() >= 96 {
		p = netip.PrefixFrom(p.Addr().Unmap(), p.Bits()-96)
	}
	return p.Masked()
}

// Ranges returns the ranges in the list.
func (d DenyList) Ranges() []netip.Prefix { return slices.Clone(d.ranges) }

// Denies reports whether addr is in one of the ranges.
func (d DenyList) Denies(addr netip.Addr) bool {
	addr = addr.Unmap()
	for _, p := range d.ranges {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

// Reachable reports whether the edge may connect to addr: a public destination the list does
// not deny.
func (d DenyList) Reachable(addr netip.Addr) bool {
	return IsPublicDestination(addr) && !d.Denies(addr)
}

// Destinations keeps the addresses of addrs the edge may connect to: the public ones (see
// PublicAddresses) the list does not deny, in order. When none is left, the reason says why:
// ReasonDeniedDestination when the list removed at least one public address, so the operator
// sees their setting at work, and ReasonForbiddenDestination otherwise.
func (d DenyList) Destinations(addrs []netip.Addr) ([]netip.Addr, Reason) {
	public := PublicAddresses(addrs)
	if len(public) == 0 {
		return nil, ReasonForbiddenDestination
	}
	kept := slices.DeleteFunc(public, d.Denies)
	if len(kept) == 0 {
		return nil, ReasonDeniedDestination
	}
	return kept, ""
}
