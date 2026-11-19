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

// Peer is what the kernel says about the other end of a Unix socket
// (SO_PEERCRED). It cannot be forged by the caller.
type Peer struct {
	PID int
	UID int
}

// HumanByUID finds the human account that owns a Linux uid.
type HumanByUID func(uid int) (name string, ok bool)

// ResolvePeer turns socket peer credentials and the peer's cgroup file into
// a caller. An agent is recognised only by its system unit
// agent@<name>.service, so several uid-0 agents stay distinct; then a human
// account by uid; anything else is a system caller.
func ResolvePeer(p Peer, cgroup string, humanByUID HumanByUID) core.Caller {
	root := p.UID == 0
	if name, ok := AgentFromCgroup(cgroup); ok {
		return core.Caller{Kind: core.CallerAgent, Name: name, UID: p.UID, IsRoot: root}
	}
	if name, ok := humanByUID(p.UID); ok && !root {
		return core.Caller{Kind: core.CallerHuman, Name: name, UID: p.UID}
	}
	name := SystemUnitFromCgroup(cgroup)
	if name == "" {
		name = "uid:" + strconv.Itoa(p.UID)
	}
	return core.Caller{Kind: core.CallerSystem, Name: name, UID: p.UID, IsRoot: root}
}

// AgentFromCgroup returns the agent name when the cgroup v2 path is inside
// the system unit agent@<name>.service. Units of a user's own systemd
// manager live under user.slice and never match, so a human cannot pose as
// an agent by naming a unit.
func AgentFromCgroup(cgroup string) (string, bool) {
	unit, ok := systemUnit(cgroup)
	if !ok {
		return "", false
	}
	name, found := strings.CutPrefix(unit, "agent@")
	if !found {
		return "", false
	}
	name, found = strings.CutSuffix(name, ".service")
	if !found || !agentName.MatchString(name) {
		return "", false
	}
	return name, true
}

// SystemUnitFromCgroup returns the system unit that holds the process, or
// "" outside system.slice.
func SystemUnitFromCgroup(cgroup string) string {
	unit, _ := systemUnit(cgroup)
	return unit
}

// systemUnit finds the unit component of a cgroup v2 path under
// /system.slice, allowing one intermediate template slice
// (system-<prefix>.slice). Deeper components are the unit's own
// delegated sub-cgroups.
func systemUnit(cgroup string) (string, bool) {
	path, ok := unifiedPath(cgroup)
	if !ok {
		return "", false
	}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 2 || parts[0] != "system.slice" {
		return "", false
	}
	unit := parts[1]
	if strings.HasSuffix(unit, ".slice") {
		if len(parts) < 3 {
			return "", false
		}
		unit = parts[2]
	}
	if strings.HasSuffix(unit, ".slice") {
		return "", false
	}
	return unit, true
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
