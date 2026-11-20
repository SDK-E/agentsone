// SPDX-License-Identifier: MIT

// Package peer reads who is on the other end of a Unix socket: the kernel's
// SO_PEERCRED (Linux only) and the peer process's cgroup from /proc.
package peer

import (
	"context"
	"errors"
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
	// Proc is the /proc file system (ProcFS()), or nil where there is none.
	Proc fs.FS
	// Credentials reads SO_PEERCRED; Credentials in production.
	Credentials func(net.Conn) (domain.Peer, error)
	// Directory finds the agent or human who owns a uid.
	Directory domain.Directory
	// DaemonPID is the daemon's own pid (os.Getpid()).
	DaemonPID int
}

// Caller resolves the peer of c, or refuses it with a *domain.Refusal. It
// reads the cgroup at accept time, while the peer process is still
// connected, to keep pid reuse out of reach.
func (r Resolver) Caller(c net.Conn) (core.Caller, error) {
	p, err := r.Credentials(c)
	if err != nil {
		return core.Anonymous, &domain.Refusal{Peer: domain.Peer{PID: -1, UID: -1}, Reason: domain.ReasonNoCredentials, Err: err}
	}
	// The peer is still connected while this runs: the lookups are bounded
	// by the connection, not by any one request.
	ctx := context.Background()
	if r.Proc == nil {
		// No cgroups on this platform: nobody is placed as an agent.
		return domain.ResolvePeer(ctx, p, "", r.DaemonPID, r.Directory)
	}
	cgroup, err := fs.ReadFile(r.Proc, strconv.Itoa(p.PID)+"/cgroup")
	if err != nil {
		return core.Anonymous, &domain.Refusal{Peer: p, Reason: domain.ReasonNoCgroup, Err: err}
	}
	return domain.ResolvePeer(ctx, p, string(cgroup), r.DaemonPID, r.Directory)
}

// ConnContext is an http.Server ConnContext that stores the resolved
// caller, or the refusal, with store. Requests on a refused connection are
// refused unless they carry their own credentials.
func (r Resolver) ConnContext(store func(context.Context, core.Caller, error) context.Context) func(context.Context, net.Conn) context.Context {
	return func(ctx context.Context, c net.Conn) context.Context {
		caller, err := r.Caller(c)
		return store(ctx, caller, err)
	}
}
