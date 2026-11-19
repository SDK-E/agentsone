// SPDX-License-Identifier: MIT

package process

import (
	"errors"
	"fmt"
	"io"
	"strconv"
)

// LaunchArgv0 is argv[0] of the launch helper. The daemon starts a custom
// agent's runner as /proc/self/exe with this argv[0]; the helper joins the
// agent's cgroup, lowers its resource limits, drops to the agent's user
// and groups, then execs the runner in the same process. So the runner is
// inside its cgroup, limited and unprivileged from its first instruction,
// with no clone3 and no ptrace (both blocked by Docker's default seccomp
// profile). runc's init works the same way.
const LaunchArgv0 = "agentsone-agent-launch"

// LaunchFailed is the exit code of a helper that could not set up the
// runner. Its reason goes to the report pipe (file descriptor 3).
const LaunchFailed = 126

// rlimitKind names a resource limit without depending on the platform.
type rlimitKind int

const (
	rlimitCore rlimitKind = iota
	rlimitNoFile
	rlimitNProc
	rlimitAS
)

func (k rlimitKind) String() string {
	return [...]string{"core", "nofile", "nproc", "as"}[k]
}

// launchPlan is everything the helper does, in order. It travels as
// command-line arguments, so the helper needs no shared state.
type launchPlan struct {
	Cgroup string // the cgroup directory to join, or "" for none
	UID    int    // the user to become, or -1 to keep the daemon's
	GID    int    // the group to become, or -1 to keep the daemon's
	NoFile uint64 // RLIMIT_NOFILE, or 0 to leave it
	NProc  uint64 // RLIMIT_NPROC, or 0 to leave it
	AS     uint64 // RLIMIT_AS, or 0 to leave it
	Path   string // the runner executable
	Args   []string
}

var errBadLaunch = errors.New("bad launch arguments")

// args encodes p as the helper's arguments (after argv[0]).
func (p launchPlan) args() []string {
	a := []string{
		"--cgroup", p.Cgroup,
		"--uid", strconv.Itoa(p.UID), "--gid", strconv.Itoa(p.GID),
		"--nofile", strconv.FormatUint(p.NoFile, 10),
		"--nproc", strconv.FormatUint(p.NProc, 10),
		"--as", strconv.FormatUint(p.AS, 10),
		"--", p.Path,
	}
	return append(a, p.Args...)
}

// parseLaunch decodes the helper's arguments. Every option is required,
// so a plan never runs half-specified.
func parseLaunch(args []string) (launchPlan, error) {
	p := launchPlan{}
	seen := map[string]bool{}
	for len(args) > 0 && args[0] != "--" {
		if len(args) < 2 {
			return p, fmt.Errorf("%w: %s has no value", errBadLaunch, args[0])
		}
		name, v := args[0], args[1]
		args = args[2:]
		var err error
		switch name {
		case "--cgroup":
			p.Cgroup = v
		case "--uid":
			p.UID, err = strconv.Atoi(v)
		case "--gid":
			p.GID, err = strconv.Atoi(v)
		case "--nofile":
			p.NoFile, err = strconv.ParseUint(v, 10, 64)
		case "--nproc":
			p.NProc, err = strconv.ParseUint(v, 10, 64)
		case "--as":
			p.AS, err = strconv.ParseUint(v, 10, 64)
		default:
			return p, fmt.Errorf("%w: unknown option %s", errBadLaunch, name)
		}
		if err != nil {
			return p, fmt.Errorf("%w: %s %q", errBadLaunch, name, v)
		}
		seen[name] = true
	}
	if len(seen) != 6 {
		return p, fmt.Errorf("%w: want all six options, got %d", errBadLaunch, len(seen))
	}
	if len(args) < 2 || args[1] == "" {
		return p, fmt.Errorf("%w: no runner after --", errBadLaunch)
	}
	if (p.UID < 0) != (p.GID < 0) || p.UID == 0 {
		return p, fmt.Errorf("%w: uid %d gid %d", errBadLaunch, p.UID, p.GID)
	}
	p.Path, p.Args = args[1], args[2:]
	return p, nil
}

// launchSys is what the helper asks of the operating system.
type launchSys interface {
	JoinCgroup(dir string) error
	// LowerLimit sets a resource limit to v, or to the current hard
	// limit when v is above it: the helper only ever lowers limits.
	LowerLimit(k rlimitKind, v uint64) error
	SetIDs(uid, gid int) error // groups, then gid, then uid
	Exec(path string, argv []string) error
}

// runLaunch carries out p. It returns only on failure: on success Exec
// replaces the process with the runner.
func runLaunch(p launchPlan, sys launchSys) error {
	if p.Cgroup != "" {
		if err := sys.JoinCgroup(p.Cgroup); err != nil {
			return fmt.Errorf("join cgroup %s: %w", p.Cgroup, err)
		}
	}
	limits := []struct {
		k rlimitKind
		v uint64
	}{{rlimitCore, 0}, {rlimitNoFile, p.NoFile}, {rlimitNProc, p.NProc}, {rlimitAS, p.AS}}
	for _, l := range limits {
		if l.v == 0 && l.k != rlimitCore {
			continue
		}
		if err := sys.LowerLimit(l.k, l.v); err != nil {
			return fmt.Errorf("set %s limit: %w", l.k, err)
		}
	}
	if p.UID > 0 {
		if err := sys.SetIDs(p.UID, p.GID); err != nil {
			return fmt.Errorf("become uid %d gid %d: %w", p.UID, p.GID, err)
		}
	}
	if err := sys.Exec(p.Path, append([]string{p.Path}, p.Args...)); err != nil {
		return fmt.Errorf("exec %s: %w", p.Path, err)
	}
	return nil
}

// launchMain is the helper's main. It writes why it failed to report and
// returns LaunchFailed; it does not return when the runner starts.
func launchMain(args []string, sys launchSys, report io.Writer) int {
	p, err := parseLaunch(args)
	if err == nil {
		err = runLaunch(p, sys)
	}
	if err == nil {
		err = errors.New("exec returned without an error")
	}
	_, _ = io.WriteString(report, err.Error())
	return LaunchFailed
}

// lowered is v, or hard when v is above it.
func lowered(v, hard uint64) uint64 {
	if v > hard {
		return hard
	}
	return v
}
