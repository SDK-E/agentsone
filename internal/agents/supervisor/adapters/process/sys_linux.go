// SPDX-License-Identifier: MIT

//go:build linux

package process

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/SDK-E/agentsone/internal/agents/supervisor/domain"
	"github.com/SDK-E/agentsone/internal/core"
)

// selfExe is the running binary; it holds the launch helper.
const selfExe = "/proc/self/exe"

// sysState is what prepare set up for one process.
type sysState struct {
	cgroup string // the agent's cgroup directory, or ""
	mode   domain.LimitMode
	report *os.File // read end of the helper's report pipe, or nil
	write  *os.File // the daemon's copy of its write end, or nil
}

// prepare turns cmd into a start of the launch helper for a custom agent:
// the helper joins the agent's cgroup, lowers its rlimits and drops to its
// user before it execs the runner, all in the same process. A root agent's
// runner starts directly, without limits.
func prepare(cmd *exec.Cmd, spec domain.UnitSpec, cgroupRoot string) (*sysState, error) {
	st := &sysState{mode: domain.LimitsNone}
	if spec.Account.IsRoot {
		return st, nil
	}
	plan := launchPlan{UID: -1, GID: -1, NoFile: DefaultNoFile, Path: cmd.Path, Args: cmd.Args[1:]}
	if os.Geteuid() == 0 {
		uid, gid, err := ids(spec.Account)
		if err != nil {
			return nil, err
		}
		plan.UID, plan.GID = uid, gid
	}
	if spec.Limits != (domain.Limits{}) {
		st.mode = domain.LimitsSoft
		if dir := agentCgroup(cgroupRoot, spec); dir != "" {
			st.cgroup, st.mode, plan.Cgroup = dir, domain.LimitsHard, dir
		} else {
			// Without a cgroup the rlimits are the limits, and CPU has
			// none. Memory is RLIMIT_DATA, the private writable memory of
			// each process, so each process may use memory_mb, not the
			// agent as a whole. Never RLIMIT_AS: address space is not
			// memory, and Go, Node and Java reserve far more of it than
			// they use, so the runner died before main. Tasks are the
			// processes of the agent's user. With a cgroup, memory.max and
			// pids.max are exact and per agent.
			plan.Data = uint64(max(spec.Limits.MemoryMaxBytes, 0))
			plan.NProc = uint64(max(spec.Limits.TasksMax, 0))
		}
	}
	r, w, err := os.Pipe()
	if err != nil {
		st.cleanup()
		return nil, fmt.Errorf("report pipe: %w", err)
	}
	st.report, st.write = r, w
	cmd.Path = selfExe
	cmd.Args = append([]string{LaunchArgv0}, plan.args()...)
	cmd.ExtraFiles = []*os.File{w} // the helper's file descriptor 3
	return st, nil
}

// agentCgroup creates the agent's cgroup with its limits under root, or
// returns "" when root is unset or not writable: then limits are soft.
func agentCgroup(root string, spec domain.UnitSpec) string {
	if root == "" {
		return ""
	}
	dir := filepath.Join(root, spec.Agent)
	if err := os.Mkdir(dir, 0o755); err != nil && !os.IsExist(err) { // #nosec G301 -- cgroupfs directory
		return ""
	}
	if err := writeLimits(dir, spec.Limits); err != nil {
		_ = os.Remove(dir)
		return ""
	}
	return dir
}

// cellFor is what a terminal of the agent needs to count as its runner,
// as prepare gives the runner: the agent's cgroup with its limits, or
// without one the runner's process limits. A custom agent also gets the
// runner's open-file limit. A root agent gets its cgroup without limits,
// or nothing.
func cellFor(root string, spec domain.UnitSpec) core.ProcessCell {
	c := core.ProcessCell{}
	if !spec.Account.IsRoot {
		c.NoFile = DefaultNoFile
	}
	if dir := agentCgroup(root, spec); dir != "" {
		c.Cgroup = dir
		return c
	}
	if !spec.Account.IsRoot {
		c.Data = uint64(max(spec.Limits.MemoryMaxBytes, 0))
		c.NProc = uint64(max(spec.Limits.TasksMax, 0))
	}
	return c
}

// humanCgroup makes parent and parent/name when they are missing and
// returns parent/name. Only root can make a cgroup, so each one is root's;
// one that group or others may write is refused, as no process could then
// be trusted to be the human's.
func humanCgroup(parent, name string) (string, error) {
	dir := filepath.Join(parent, name)
	for _, d := range []string{parent, dir} {
		if err := os.Mkdir(d, 0o755); err != nil && !os.IsExist(err) { // #nosec G301 -- cgroupfs directory
			return "", fmt.Errorf("make cgroup %s: %w", d, err)
		}
		info, err := os.Stat(d)
		if err != nil {
			return "", fmt.Errorf("check cgroup %s: %w", d, err)
		}
		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok || st.Uid != 0 || info.Mode().Perm()&0o022 != 0 {
			return "", fmt.Errorf("cgroup %s: mode %v owner %d: %w", d, info.Mode().Perm(), st.Uid, errNotRootOwned)
		}
	}
	return dir, nil
}

// errNotRootOwned is a human cgroup someone other than root may write.
var errNotRootOwned = errors.New("not root-owned or writable by group or others")

// Placement is how the launch helper sets up a program before it runs:
// the cell to enter, then the user to become.
type Placement struct {
	Cell core.ProcessCell
	// UID and GID are the user to become, or both -1 to stay the daemon's
	// user. Never 0: a root program keeps the daemon's root.
	UID, GID int
}

// Placed is a command rewritten to start through the launch helper.
type Placed struct{ st *sysState }

// Place rewrites cmd to start through the launch helper. The helper joins
// the cell's cgroup, lowers its limits and becomes the placement's user,
// then execs cmd's program in the same process, so the program runs placed
// from its first instruction and so does everything it starts. Call
// Started right after cmd starts, or Close when it does not start.
func Place(cmd *exec.Cmd, p Placement) (*Placed, error) {
	if (p.UID < 0) != (p.GID < 0) || p.UID == 0 {
		return nil, fmt.Errorf("%w: uid %d gid %d", errBadLaunch, p.UID, p.GID)
	}
	plan := launchPlan{
		Cgroup: p.Cell.Cgroup, UID: p.UID, GID: p.GID,
		NoFile: p.Cell.NoFile, NProc: p.Cell.NProc, Data: p.Cell.Data,
		Path: cmd.Path, Args: cmd.Args[1:],
	}
	r, w, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("report pipe: %w", err)
	}
	cmd.Path = selfExe
	cmd.Args = append([]string{LaunchArgv0}, plan.args()...)
	cmd.ExtraFiles = []*os.File{w} // the helper's file descriptor 3
	return &Placed{st: &sysState{report: r, write: w}}, nil
}

// Started waits until the helper has exec'd the program, or returns why
// it failed. The helper then exits with LaunchFailed.
func (p *Placed) Started() error {
	_, err := p.st.afterStart()
	return err
}

// Close closes the report pipe that Started did not read.
func (p *Placed) Close() { p.st.cleanup() }

func ids(a domain.Account) (int, int, error) {
	u, err := user.Lookup(a.User)
	if err != nil {
		return 0, 0, fmt.Errorf("look up user %s: %w", a.User, err)
	}
	uid, err := strconv.Atoi(u.Uid)
	if err != nil {
		return 0, 0, fmt.Errorf("uid of %s: %w", a.User, err)
	}
	gid, err := strconv.Atoi(u.Gid)
	if err != nil {
		return 0, 0, fmt.Errorf("gid of %s: %w", a.User, err)
	}
	if uid == 0 {
		return 0, 0, fmt.Errorf("custom agent user %s has uid 0", a.User)
	}
	return uid, gid, nil
}

func writeLimits(dir string, l domain.Limits) error {
	write := func(name, v string) error {
		return os.WriteFile(filepath.Join(dir, name), []byte(v), 0o644) // #nosec G306 -- cgroupfs control file
	}
	if l.CPUQuotaPercent > 0 {
		if err := write("cpu.max", strconv.Itoa(l.CPUQuotaPercent*1000)+" 100000"); err != nil {
			return err
		}
	}
	if l.MemoryMaxBytes > 0 {
		if err := write("memory.max", strconv.FormatInt(l.MemoryMaxBytes, 10)); err != nil {
			return err
		}
		// No swap, so the memory limit is the whole limit. The file is
		// missing when the kernel has no swap accounting; that is fine.
		if err := write("memory.swap.max", "0"); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	if l.TasksMax > 0 {
		if err := write("pids.max", strconv.Itoa(l.TasksMax)); err != nil {
			return err
		}
	}
	return nil
}

// errLaunch is a launch helper that failed before the runner started.
var errLaunch = errors.New("launch helper failed")

// afterStart waits until the launch helper has exec'd the runner (the
// report pipe carries launchReady, then closes on exec) or failed (the
// pipe carries why, or nothing when the helper died first). It returns
// how the runner's limits hold.
func (st *sysState) afterStart() (domain.LimitMode, error) {
	if st.report == nil {
		return st.mode, nil
	}
	_ = st.write.Close() // else the pipe never reads EOF
	st.write = nil
	msg, err := io.ReadAll(st.report)
	_ = st.report.Close()
	st.report = nil
	if err != nil {
		return "", fmt.Errorf("read launch report: %w", err)
	}
	return st.mode, readReport(string(msg))
}

// readReport reads what the launch helper wrote to its report pipe.
func readReport(msg string) error {
	switch msg {
	case launchReady:
		return nil
	case "":
		return fmt.Errorf("%w: it ended before it started the program", errLaunch)
	default:
		return fmt.Errorf("%w: %s", errLaunch, strings.TrimPrefix(msg, launchReady))
	}
}

// cleanup closes what start did not use and removes the agent's cgroup
// once its processes are gone.
func (st *sysState) cleanup() {
	for _, f := range []**os.File{&st.report, &st.write} {
		if *f != nil {
			_ = (*f).Close()
			*f = nil
		}
	}
	if st.cgroup != "" {
		_ = os.Remove(st.cgroup)
	}
}
