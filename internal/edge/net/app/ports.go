// SPDX-License-Identifier: MIT

// Package app holds the use cases of the edge internet filter: start the filter, reload the allow
// list, answer a DNS query, route an egress request, record what happened, and freeze or release
// all egress. It talks to the outside world only through the ports in this file.
package app

import (
	"context"
	"errors"
	"net/netip"
	"time"

	"github.com/SDK-E/agentsone/internal/edge/net/domain"
)

// ErrNameNotFound is returned (wrapped) by a Resolver when the upstream answer says the name
// does not exist.
var ErrNameNotFound = errors.New("name not found upstream")

// Clock tells the time. Tests use a fake clock moved by hand.
type Clock interface {
	Now() time.Time
}

// Resolver looks names up from the edge, on its outer network.
type Resolver interface {
	// LookupIPv4 returns the IPv4 addresses of host, or an error wrapping ErrNameNotFound.
	LookupIPv4(ctx context.Context, host string) ([]netip.Addr, error)
}

// AuditObserver receives a record of every DNS query and every egress request, allowed or
// refused. What the edge sees is proof: the machine cannot forge or hide these records.
// Implementations must return quickly; they are called on the request path.
type AuditObserver interface {
	Record(Event)
}

// Event is one DNS query or egress request the edge decided.
type Event struct {
	At time.Time
	// Kind is "dns" for a query, or the entry point of an egress request: "http", "tls" or
	// "proxy".
	Kind string
	// Source is the machine address the request came from.
	Source netip.AddrPort
	// Name is the name asked for, as the machine sent it when it was not a valid name.
	Name string
	// Port is the destination port of an egress request; zero for a query.
	Port    uint16
	Allowed bool
	Reason  string
	// Suppressed counts records dropped by the audit rate limit just before this one.
	Suppressed int
}

// AllowListStore keeps the allow list on disk.
type AllowListStore interface {
	// Init writes the default list when no list exists yet. It never changes an existing list.
	Init(ctx context.Context) error
	// Load returns the current list. A missing list loads as the default list.
	Load(ctx context.Context) (domain.AllowList, error)
	// Update applies change to the current list and saves the result, as one step that no other
	// Update can interleave with.
	Update(ctx context.Context, change func(domain.AllowList) (domain.AllowList, error)) (domain.AllowList, error)
}

// Watcher tells when the stored allow list may have changed.
type Watcher interface {
	// Watch calls onChange once when the watch is in place, then after each change until ctx is
	// done, then returns.
	Watch(ctx context.Context, onChange func()) error
}

// FreezeStore keeps the freeze switch, so a restarted edge comes back frozen if it was.
type FreezeStore interface {
	Load(ctx context.Context) (bool, error)
	Save(ctx context.Context, frozen bool) error
}

// Freezer is the edge-local freeze lever, shaped like the daemon's freezer but with one scope:
// all egress. Freezing refuses every new egress request and cuts every open one.
type Freezer interface {
	Freeze(ctx context.Context) error
	Release(ctx context.Context) error
	Frozen(ctx context.Context) (bool, error)
}
