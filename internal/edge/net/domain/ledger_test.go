// SPDX-License-Identifier: MIT

package domain_test

import (
	"net/netip"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/SDK-E/agentsone/internal/edge/net/domain"
)

var t0 = time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)

func TestLedger_KeepsAddressUntilTTLPlusGrace(t *testing.T) {
	t.Parallel()
	addr := netip.MustParseAddr("140.82.121.4")
	tests := []struct {
		name  string
		after time.Duration
		want  []string
	}{
		{"just recorded", 0, []string{"github.com"}},
		{"inside the grace", 60*time.Second + 29*time.Second, []string{"github.com"}},
		{"one nanosecond before expiry", 90*time.Second - time.Nanosecond, []string{"github.com"}},
		{"exactly at expiry", 90 * time.Second, nil},
		{"after expiry", 91 * time.Second, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			l := domain.NewLedger(30*time.Second, 16)
			l.Record(mustName(t, "github.com"), []netip.Addr{addr}, time.Minute, t0)

			got := names(l.Names(addr, t0.Add(tt.after)))

			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Fatalf("Names() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestLedger_UnknownAddressHasNoNames(t *testing.T) {
	t.Parallel()
	l := domain.NewLedger(30*time.Second, 16)
	if got := l.Names(netip.MustParseAddr("1.1.1.1"), t0); len(got) != 0 {
		t.Fatalf("Names() = %v, want none", got)
	}
}

func TestLedger_RecordingAgainExtendsExpiry(t *testing.T) {
	t.Parallel()
	addr := netip.MustParseAddr("140.82.121.4")
	l := domain.NewLedger(30*time.Second, 16)
	l.Record(mustName(t, "github.com"), []netip.Addr{addr}, time.Minute, t0)
	l.Record(mustName(t, "github.com"), []netip.Addr{addr}, time.Minute, t0.Add(time.Minute))

	got := names(l.Names(addr, t0.Add(2*time.Minute)))

	if diff := cmp.Diff([]string{"github.com"}, got); diff != "" {
		t.Fatalf("Names() mismatch (-want +got):\n%s", diff)
	}
}

func TestLedger_ShorterAnswerDoesNotShortenExpiry(t *testing.T) {
	t.Parallel()
	addr := netip.MustParseAddr("140.82.121.4")
	l := domain.NewLedger(0, 16)
	l.Record(mustName(t, "github.com"), []netip.Addr{addr}, time.Hour, t0)
	l.Record(mustName(t, "github.com"), []netip.Addr{addr}, time.Second, t0)

	if got := l.Names(addr, t0.Add(time.Minute)); len(got) != 1 {
		t.Fatalf("Names() = %v, want github.com still recorded", got)
	}
}

func TestLedger_ListsEveryNameSharingAnAddressSorted(t *testing.T) {
	t.Parallel()
	addr := netip.MustParseAddr("185.199.108.133")
	l := domain.NewLedger(30*time.Second, 16)
	l.Record(mustName(t, "raw.githubusercontent.com"), []netip.Addr{addr}, time.Minute, t0)
	l.Record(mustName(t, "objects.githubusercontent.com"), []netip.Addr{addr}, time.Minute, t0)

	got := names(l.Names(addr, t0))

	want := []string{"objects.githubusercontent.com", "raw.githubusercontent.com"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("Names() mismatch (-want +got):\n%s", diff)
	}
}

func TestLedger_EvictsSoonestExpiringAddressWhenFull(t *testing.T) {
	t.Parallel()
	a := netip.MustParseAddr("1.0.0.1")
	b := netip.MustParseAddr("1.0.0.2")
	c := netip.MustParseAddr("1.0.0.3")
	l := domain.NewLedger(0, 2)
	l.Record(mustName(t, "a.example.com"), []netip.Addr{a}, time.Minute, t0)
	l.Record(mustName(t, "b.example.com"), []netip.Addr{b}, time.Hour, t0)

	l.Record(mustName(t, "c.example.com"), []netip.Addr{c}, time.Hour, t0)

	got := []int{len(l.Names(a, t0)), len(l.Names(b, t0)), len(l.Names(c, t0))}
	if diff := cmp.Diff([]int{0, 1, 1}, got); diff != "" {
		t.Fatalf("names per address mismatch (-want +got):\n%s", diff)
	}
}

func TestLedger_DropsExpiredAddressesBeforeEvictingLiveOnes(t *testing.T) {
	t.Parallel()
	a := netip.MustParseAddr("1.0.0.1")
	b := netip.MustParseAddr("1.0.0.2")
	c := netip.MustParseAddr("1.0.0.3")
	l := domain.NewLedger(0, 2)
	l.Record(mustName(t, "a.example.com"), []netip.Addr{a}, time.Hour, t0)
	l.Record(mustName(t, "b.example.com"), []netip.Addr{b}, time.Second, t0)

	later := t0.Add(time.Minute)
	l.Record(mustName(t, "c.example.com"), []netip.Addr{c}, time.Hour, later)

	got := []int{len(l.Names(a, later)), len(l.Names(c, later))}
	if diff := cmp.Diff([]int{1, 1}, got); diff != "" {
		t.Fatalf("names per address mismatch (-want +got):\n%s", diff)
	}
}

func TestLedger_AddrsListsLiveAddressesOfAName(t *testing.T) {
	t.Parallel()
	l := domain.NewLedger(0, 16)
	l.Record(mustName(t, "github.com"), []netip.Addr{netip.MustParseAddr("140.82.121.4"), netip.MustParseAddr("140.82.121.3")}, time.Minute, t0)
	l.Record(mustName(t, "example.com"), []netip.Addr{netip.MustParseAddr("93.184.215.14")}, time.Minute, t0)

	got := l.Addrs(mustName(t, "github.com"), t0)

	want := []netip.Addr{netip.MustParseAddr("140.82.121.3"), netip.MustParseAddr("140.82.121.4")}
	if diff := cmp.Diff(want, got, cmp.Comparer(func(a, b netip.Addr) bool { return a == b })); diff != "" {
		t.Fatalf("Addrs() mismatch (-want +got):\n%s", diff)
	}
}

func TestLedger_AddrsForgetsExpiredAddresses(t *testing.T) {
	t.Parallel()
	l := domain.NewLedger(0, 16)
	l.Record(mustName(t, "github.com"), []netip.Addr{netip.MustParseAddr("140.82.121.4")}, time.Minute, t0)

	if got := l.Addrs(mustName(t, "github.com"), t0.Add(time.Minute)); len(got) != 0 {
		t.Fatalf("Addrs() at expiry = %v, want none", got)
	}
}

func names(ns []domain.Name) []string {
	var out []string
	for _, n := range ns {
		out = append(out, n.String())
	}
	return out
}
