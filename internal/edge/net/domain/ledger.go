// SPDX-License-Identifier: MIT

package domain

import (
	"net/netip"
	"slices"
	"strings"
	"time"
)

// Ledger records which addresses the edge resolved for which allowed names, and until when. The
// egress proxies connect only to addresses in the ledger for the name the machine asked for, so a
// connection can never be steered to an address that was not resolved for an allowed name. An
// entry lives for the resolve TTL plus a grace period.
//
// The ledger holds at most capacity addresses. When it is full it first drops expired addresses,
// then the address that would expire soonest, so a flood of lookups cannot grow host memory.
//
// A Ledger is not safe for concurrent use; the app layer serialises access.
type Ledger struct {
	grace    time.Duration
	capacity int
	byAddr   map[netip.Addr]map[Name]time.Time
}

// NewLedger makes an empty ledger. capacity below 1 is treated as 1.
func NewLedger(grace time.Duration, capacity int) *Ledger {
	return &Ledger{grace: grace, capacity: max(capacity, 1), byAddr: make(map[netip.Addr]map[Name]time.Time)}
}

// Record notes that addrs were resolved for name at now with the given TTL. Recording an address
// again never shortens its expiry.
func (l *Ledger) Record(name Name, addrs []netip.Addr, ttl time.Duration, now time.Time) {
	expiry := now.Add(ttl + l.grace)
	for _, addr := range addrs {
		names, ok := l.byAddr[addr]
		if !ok {
			l.makeRoom(now)
			names = make(map[Name]time.Time)
			l.byAddr[addr] = names
		}
		if expiry.After(names[name]) {
			names[name] = expiry
		}
	}
}

// Names returns the names whose entry for addr has not expired at now, sorted.
func (l *Ledger) Names(addr netip.Addr, now time.Time) []Name {
	var out []Name
	for name, expiry := range l.byAddr[addr] {
		if now.Before(expiry) {
			out = append(out, name)
		}
	}
	slices.SortFunc(out, func(a, b Name) int { return strings.Compare(a.s, b.s) })
	return out
}

// Addrs returns the addresses recorded for name that have not expired at now, sorted.
func (l *Ledger) Addrs(name Name, now time.Time) []netip.Addr {
	var out []netip.Addr
	for addr, names := range l.byAddr {
		if expiry, ok := names[name]; ok && now.Before(expiry) {
			out = append(out, addr)
		}
	}
	slices.SortFunc(out, func(a, b netip.Addr) int { return a.Compare(b) })
	return out
}

func (l *Ledger) makeRoom(now time.Time) {
	if len(l.byAddr) < l.capacity {
		return
	}
	for addr, names := range l.byAddr {
		for name, expiry := range names {
			if !now.Before(expiry) {
				delete(names, name)
			}
		}
		if len(names) == 0 {
			delete(l.byAddr, addr)
		}
	}
	for len(l.byAddr) >= l.capacity {
		l.evictSoonest()
	}
}

func (l *Ledger) evictSoonest() {
	var victim netip.Addr
	var soonest time.Time
	for addr, names := range l.byAddr {
		latest := latestExpiry(names)
		if !victim.IsValid() || latest.Before(soonest) {
			victim, soonest = addr, latest
		}
	}
	delete(l.byAddr, victim)
}

func latestExpiry(names map[Name]time.Time) time.Time {
	var latest time.Time
	for _, expiry := range names {
		if expiry.After(latest) {
			latest = expiry
		}
	}
	return latest
}
