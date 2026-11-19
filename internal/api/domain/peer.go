// SPDX-License-Identifier: MIT

package domain

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/SDK-E/agentsone/internal/core"
)

// agentName is the contract's AgentName pattern. Fixed lookup table.
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

// AgentByUID finds the custom agent that owns a Linux uid.
type AgentByUID func(uid int) (name string, ok bool)

// ResolvePeer turns socket peer credentials and the peer's cgroup file into
// a caller. In order:
//
//  1. Hard: the process is in the agent's cgroup /agents/<name> (tools a
//     runner spawns inherit it). Several uid-0 agents stay distinct.
//  2. Soft, where cgroups cannot be placed: a uid in the custom agent range
//     that the directory knows.
//  3. Anything else is a system caller. A uid-0 process outside an agent
//     cgroup is never an agent; root agents then prove who they are with
//     their bearer token.
func ResolvePeer(p Peer, cgroup string, agentByUID AgentByUID) core.Caller {
	root := p.UID == 0
	if name, ok := AgentFromCgroup(cgroup); ok {
		return core.Caller{Kind: core.CallerAgent, Name: name, UID: p.UID, IsRoot: root}
	}
	if p.UID >= MinAgentUID && p.UID <= MaxAgentUID {
		if name, ok := agentByUID(p.UID); ok {
			return core.Caller{Kind: core.CallerAgent, Name: name, UID: p.UID, Assurance: core.AssuranceSoft}
		}
	}
	name := "uid:" + strconv.Itoa(p.UID)
	if path, ok := unifiedPath(cgroup); ok && path == "/daemon" {
		name = "daemon"
	}
	return core.Caller{Kind: core.CallerSystem, Name: name, UID: p.UID, IsRoot: root}
}

// AgentFromCgroup returns the agent name when the cgroup v2 path is
// /agents/<name> or below it.
func AgentFromCgroup(cgroup string) (string, bool) {
	path, ok := unifiedPath(cgroup)
	if !ok {
		return "", false
	}
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	if len(parts) < 2 || parts[0] != "agents" || !agentName.MatchString(parts[1]) {
		return "", false
	}
	return parts[1], true
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
