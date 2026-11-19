// SPDX-License-Identifier: MIT

// Package peer reads who is on the other end of a Unix socket: the kernel's
// SO_PEERCRED (Linux only) and the peer process's cgroup from /proc.
package peer

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"strconv"

	"github.com/SDK-E/agentsone/internal/api/domain"
	"github.com/SDK-E/agentsone/internal/core"
)

// ErrUnsupported is returned where the platform has no peer credentials.
var ErrUnsupported = errors.New("peer credentials are not supported on this platform")

// Resolver turns a Unix connection into a caller.
type Resolver struct {
	// Proc is the /proc file system (os.DirFS("/proc") in production).
	Proc fs.FS
	// Credentials reads SO_PEERCRED; Credentials in production.
	Credentials func(net.Conn) (domain.Peer, error)
	// HumanByUID finds human accounts by Linux uid. Humans live in core.db
	// and have no Linux user, so production passes NoHumans.
	HumanByUID domain.HumanByUID
}

// NoHumans finds no human for any uid.
func NoHumans(int) (string, bool) { return "", false }

// Caller resolves the peer of c. It reads the cgroup at accept time, while
// the peer process is still connected, to keep pid reuse out of reach.
func (r Resolver) Caller(c net.Conn) (core.Caller, error) {
	p, err := r.Credentials(c)
	if err != nil {
		return core.Anonymous, fmt.Errorf("peer credentials: %w", err)
	}
	cgroup, err := fs.ReadFile(r.Proc, strconv.Itoa(p.PID)+"/cgroup")
	if err != nil {
		return core.Anonymous, fmt.Errorf("peer cgroup of pid %d: %w", p.PID, err)
	}
	return domain.ResolvePeer(p, string(cgroup), r.HumanByUID), nil
}

// ConnContext is an http.Server ConnContext that stores the resolved
// caller with store, or leaves the context bare when it cannot be resolved
// (requests are then refused).
func (r Resolver) ConnContext(store func(context.Context, core.Caller) context.Context) func(context.Context, net.Conn) context.Context {
	return func(ctx context.Context, c net.Conn) context.Context {
		caller, err := r.Caller(c)
		if err != nil {
			return ctx
		}
		return store(ctx, caller)
	}
}
