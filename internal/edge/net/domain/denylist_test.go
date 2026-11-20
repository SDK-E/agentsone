// SPDX-License-Identifier: MIT

package domain_test

import (
	"net/netip"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/SDK-E/agentsone/internal/edge/net/domain"
)

func prefixes(t *testing.T, ss ...string) []netip.Prefix {
	t.Helper()
	out := make([]netip.Prefix, 0, len(ss))
	for _, s := range ss {
		out = append(out, netip.MustParsePrefix(s))
	}
	return out
}

func addrs(ss ...string) []netip.Addr {
	out := make([]netip.Addr, 0, len(ss))
	for _, s := range ss {
		out = append(out, netip.MustParseAddr(s))
	}
	return out
}

var prefixComparer = cmp.Comparer(func(a, b netip.Prefix) bool { return a == b })

func TestParseDenyRanges_ReadsRangesAndSingleAddresses(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   string
		want []netip.Prefix
	}{
		{"empty denies nothing", "", nil},
		{"blank items are skipped", " , ,", nil},
		{"ipv4 and ipv6 ranges", "203.0.113.0/24,2001:db8::/48", prefixes(t, "203.0.113.0/24", "2001:db8::/48")},
		{"spaces around items are trimmed", " 203.0.113.7/32 , 2001:db8::7/128 ", prefixes(t, "203.0.113.7/32", "2001:db8::7/128")},
		{"a single ipv4 address is one host", "198.51.100.7", prefixes(t, "198.51.100.7/32")},
		{"a single ipv6 address is one host", "2001:db8::7", prefixes(t, "2001:db8::7/128")},
		{"host bits are cleared, which denies the whole range", "203.0.113.9/24", prefixes(t, "203.0.113.0/24")},
		{"an ipv4-mapped range is read as ipv4", "::ffff:203.0.113.7/128", prefixes(t, "203.0.113.7/32")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := domain.ParseDenyRanges(tt.in)
			if err != nil {
				t.Fatalf("ParseDenyRanges(%q) error = %v", tt.in, err)
			}
			if diff := cmp.Diff(tt.want, got, prefixComparer); diff != "" {
				t.Fatalf("ParseDenyRanges(%q) mismatch (-want +got):\n%s", tt.in, diff)
			}
		})
	}
}

func TestParseDenyRanges_RefusesWhatIsNotARange(t *testing.T) {
	t.Parallel()
	for _, in := range []string{
		"example.com",
		"203.0.113.7/33",
		"2001:db8::/129",
		"300.1.2.3",
		"203.0.113.0/24;198.51.100.0/24",
		"fe80::1%eth0",
		"203.0.113.0/24,not-a-range",
	} {
		t.Run(in, func(t *testing.T) {
			t.Parallel()
			_, err := domain.ParseDenyRanges(in)
			if err == nil {
				t.Fatalf("ParseDenyRanges(%q) error = nil, want an error", in)
			}
			if !strings.Contains(err.Error(), "for example") {
				t.Fatalf("ParseDenyRanges(%q) error = %q, want it to show an example", in, err)
			}
		})
	}
}

func TestDenyList_Denies(t *testing.T) {
	t.Parallel()
	deny := domain.NewDenyList(prefixes(t, "203.0.113.0/24", "2001:db8::/48"))
	tests := []struct {
		addr string
		want bool
	}{
		{"203.0.113.7", true},
		{"::ffff:203.0.113.7", true},
		{"2001:db8::7", true},
		{"2001:db8:0:ffff::1", true},
		{"203.0.114.1", false},
		{"2001:db9::1", false},
		{"140.82.121.4", false},
	}
	for _, tt := range tests {
		t.Run(tt.addr, func(t *testing.T) {
			t.Parallel()
			if got := deny.Denies(netip.MustParseAddr(tt.addr)); got != tt.want {
				t.Fatalf("Denies(%s) = %v, want %v", tt.addr, got, tt.want)
			}
		})
	}
}

func TestDenyList_NormalisesRangesItIsGiven(t *testing.T) {
	t.Parallel()
	deny := domain.NewDenyList([]netip.Prefix{netip.MustParsePrefix("::ffff:203.0.113.9/120")})

	if !deny.Denies(netip.MustParseAddr("203.0.113.200")) {
		t.Fatal("Denies(203.0.113.200) = false, want the mapped range to deny the ipv4 range")
	}
}

func TestDenyList_Reachable(t *testing.T) {
	t.Parallel()
	deny := domain.NewDenyList(prefixes(t, "203.0.113.0/24", "2001:db8::/48"))
	tests := []struct {
		addr string
		want bool
	}{
		{"140.82.121.4", true},
		{"::ffff:140.82.121.4", true},
		{"203.0.113.7", false},
		{"::ffff:203.0.113.7", false},
		{"2001:db8::7", false},
		{"10.0.0.1", false},
		{"100.64.0.1", false},
	}
	for _, tt := range tests {
		t.Run(tt.addr, func(t *testing.T) {
			t.Parallel()
			if got := deny.Reachable(netip.MustParseAddr(tt.addr)); got != tt.want {
				t.Fatalf("Reachable(%s) = %v, want %v", tt.addr, got, tt.want)
			}
		})
	}
}

func TestDenyList_Destinations(t *testing.T) {
	t.Parallel()
	deny := domain.NewDenyList(prefixes(t, "203.0.113.0/24"))
	tests := []struct {
		name       string
		deny       domain.DenyList
		in         []netip.Addr
		want       []netip.Addr
		wantReason domain.Reason
	}{
		{
			name: "denied addresses are dropped, the rest kept in order",
			deny: deny,
			in:   addrs("203.0.113.7", "140.82.121.4", "::ffff:140.82.121.4", "140.82.121.3"),
			want: addrs("140.82.121.4", "140.82.121.3"),
		},
		{
			name:       "only denied addresses are refused as denied",
			deny:       deny,
			in:         addrs("203.0.113.7", "203.0.113.8"),
			wantReason: domain.ReasonDeniedDestination,
		},
		{
			name:       "denied and private addresses are refused as denied",
			deny:       deny,
			in:         addrs("10.0.0.1", "203.0.113.7"),
			wantReason: domain.ReasonDeniedDestination,
		},
		{
			name:       "only private addresses are refused as not public",
			deny:       deny,
			in:         addrs("10.0.0.1", "127.0.0.1"),
			wantReason: domain.ReasonForbiddenDestination,
		},
		{
			name: "an empty deny list keeps every public address",
			in:   addrs("203.0.113.7", "10.0.0.1"),
			want: addrs("203.0.113.7"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, reason := tt.deny.Destinations(tt.in)
			if diff := cmp.Diff(tt.want, got, cmp.Comparer(func(a, b netip.Addr) bool { return a == b })); diff != "" {
				t.Fatalf("Destinations() mismatch (-want +got):\n%s", diff)
			}
			if reason != tt.wantReason {
				t.Fatalf("Destinations() reason = %q, want %q", reason, tt.wantReason)
			}
		})
	}
}
