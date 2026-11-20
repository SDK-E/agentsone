// SPDX-License-Identifier: MIT

package domain

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/SDK-E/agentsone/internal/core"
)

// agentName is the contract's AgentName pattern, which HumanName shares.
// Fixed lookup table.
var agentName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,30}$`)

// Custom agents' Linux uids, from the agents directory in core.db.
const (
	MinAgentUID = 200000
	MaxAgentUID = 299999
)

// Peer is what the kernel says about the other end of a Unix socket
// (SO_PEERCRED). It cannot be forged by the caller.
type Peer struct {
	PID int
	UID int
}

// Directory finds who owns a Linux uid. Each lookup returns an error
// matching core.ErrNotFound when nobody does. Any other error is a failed
// lookup, and the peer is refused: a lookup never fails open.
type Directory struct {
	// AgentByUID names the custom agent whose Linux user has uid.
	AgentByUID func(ctx context.Context, uid int) (string, error)
	// HumanByUID names the human whose Linux user has uid.
	HumanByUID func(ctx context.Context, uid int) (string, error)
	// HumanByName confirms that a human called name exists: nil when one
	// does. A peer in a human cgroup is that human only then.
	HumanByName func(ctx context.Context, name string) error
}

// Why a socket peer is refused, as audit records and logs say it.
const (
	ReasonRootOutsideAgents = "uid 0 outside every agent cgroup and not the daemon"
	ReasonNoAgent           = "uid in the agent range that no agent owns"
	ReasonNobody            = "uid of no agent and no human"
	ReasonAgentLookup       = "agent directory lookup failed"
	ReasonHumanLookup       = "human directory lookup failed"
	ReasonNoHuman           = "human cgroup of no human"
	ReasonHumanCgroupLookup = "human lookup for a human cgroup failed"
	ReasonNoCredentials     = "peer credentials unreadable"
	ReasonNoCgroup          = "peer cgroup unreadable"
)

// Refusal is a socket peer that is not an agent, a human or the daemon. It
// matches core.ErrForbidden, and Err when a lookup failed.
type Refusal struct {
	Peer   Peer
	Reason string
	// Err is the failed lookup or read, or nil.
	Err error
}

func (r *Refusal) Error() string {
	s := fmt.Sprintf("socket peer pid %d uid %d refused: %s", r.Peer.PID, r.Peer.UID, r.Reason)
	if r.Err != nil {
		s += ": " + r.Err.Error()
	}
	return s
}

// Unwrap gives core.ErrForbidden and the cause.
func (r *Refusal) Unwrap() []error {
	if r.Err == nil {
		return []error{core.ErrForbidden}
	}
	return []error{core.ErrForbidden, r.Err}
}

// ResolvePeer turns socket peer credentials and the peer's cgroup file into
// a caller, or refuses the peer. daemonPID is the daemon's own pid. In order:
//
//  1. Agent, hard: the process is in the agent's cgroup /agents/<name>
//     (tools a runner spawns inherit it). Several uid-0 agents stay distinct.
//  2. The daemon: the peer is the daemon's own process. The /daemon cgroup
//     is no proof: package scripts and git run there.
//  3. Human: the process is in the human's cgroup /humans/<name>, where the
//     daemon starts that human's web terminal (as root). The caller is that
//     human, never root, with no token scopes. Soft: any root process can
//     move itself into that cgroup. A name that is no human's, or a failed
//     lookup, is refused.
//  4. Any other uid 0 is refused. Root agents outside their cgroup prove who
//     they are with their bearer token.
//  5. Agent, soft: a uid in the custom agent range that the directory knows.
//  6. Human: a uid whose Linux user is a human's. The caller is that human,
//     never root, with no token scopes. Soft: a root agent can take any uid.
//  7. Anything else is refused, and so is a peer whose lookup failed.
func ResolvePeer(ctx context.Context, p Peer, cgroup string, daemonPID int, d Directory) (core.Caller, error) {
	root := p.UID == 0
	if name, ok := AgentFromCgroup(cgroup); ok {
		return core.Caller{Kind: core.CallerAgent, Name: name, UID: p.UID, IsRoot: root}, nil
	}
	if p.PID == daemonPID {
		return core.Caller{Kind: core.CallerSystem, Name: "daemon", UID: p.UID, IsRoot: root}, nil
	}
	if name, under, ok := HumanFromCgroup(cgroup); under {
		return humanInCgroup(ctx, p, name, ok, d)
	}
	if root {
		return core.Anonymous, &Refusal{Peer: p, Reason: ReasonRootOutsideAgents}
	}
	if p.UID >= MinAgentUID && p.UID <= MaxAgentUID {
		name, err := d.AgentByUID(ctx, p.UID)
		if err != nil {
			return core.Anonymous, refusal(p, err, ReasonNoAgent, ReasonAgentLookup)
		}
		return core.Caller{Kind: core.CallerAgent, Name: name, UID: p.UID, Assurance: core.AssuranceSoft}, nil
	}
	name, err := d.HumanByUID(ctx, p.UID)
	if err != nil {
		return core.Anonymous, refusal(p, err, ReasonNobody, ReasonHumanLookup)
	}
	return core.Caller{Kind: core.CallerHuman, Name: name, UID: p.UID, Assurance: core.AssuranceSoft}, nil
}

// refusal names a lookup error: nobody owns the uid, or the lookup failed.
func refusal(p Peer, err error, notFound, failed string) *Refusal {
	if errors.Is(err, core.ErrNotFound) {
		return &Refusal{Peer: p, Reason: notFound, Err: err}
	}
	return &Refusal{Peer: p, Reason: failed, Err: err}
}

// humanInCgroup names the human whose cgroup holds the peer, or refuses
// it: a cell name no human can have, a name no human has, or a lookup that
// failed or is not wired. It never fails open.
func humanInCgroup(ctx context.Context, p Peer, name string, valid bool, d Directory) (core.Caller, error) {
	if !valid {
		return core.Anonymous, &Refusal{Peer: p, Reason: ReasonNoHuman}
	}
	if d.HumanByName == nil {
		return core.Anonymous, &Refusal{Peer: p, Reason: ReasonHumanCgroupLookup}
	}
	if err := d.HumanByName(ctx, name); err != nil {
		return core.Anonymous, refusal(p, err, ReasonNoHuman, ReasonHumanCgroupLookup)
	}
	return core.Caller{Kind: core.CallerHuman, Name: name, UID: p.UID, Assurance: core.AssuranceSoft}, nil
}

// AgentFromCgroup returns the agent name when the cgroup v2 path is
// /agents/<name> or below it.
func AgentFromCgroup(cgroup string) (string, bool) {
	name, _, ok := cellOf(cgroup, "agents")
	return name, ok
}

// HumanFromCgroup reads a cgroup v2 path under /humans. under is true for
// /humans and anything below it; ok is true, with the name, when the path
// is /humans/<name> or below it and name is a valid human name.
func HumanFromCgroup(cgroup string) (name string, under, ok bool) {
	return cellOf(cgroup, "humans")
}

// cellOf reads /<parent>/<name>[/...] from the cgroup file.
func cellOf(cgroup, parent string) (name string, under, ok bool) {
	path, found := unifiedPath(cgroup)
	if !found {
		return "", false, false
	}
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	if parts[0] != parent {
		return "", false, false
	}
	if len(parts) < 2 || !agentName.MatchString(parts[1]) {
		return "", true, false
	}
	return parts[1], true, true
}

// unifiedPath returns the cgroup v2 line ("0::<path>") of /proc/<pid>/cgroup.
func unifiedPath(cgroup string) (string, bool) {
	for line := range strings.SplitSeq(cgroup, "\n") {
		if path, ok := strings.CutPrefix(line, "0::"); ok {
			return path, true
		}
	}
	return "", false
}
