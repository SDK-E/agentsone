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

	"github.com/SDK-E/agentsone/internal/agents/supervisor/domain"
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
			// Without a cgroup the rlimits are the limits: address space
			// for memory, processes of the agent's user for tasks. With a
			// cgroup, memory.max and pids.max are exact and per agent.
			plan.AS = uint64(max(spec.Limits.MemoryMaxBytes, 0))
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
// report pipe closes with no data) or failed (the pipe carries why). It
// returns how the runner's limits hold.
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
	if len(msg) > 0 {
		return "", fmt.Errorf("%w: %s", errLaunch, msg)
	}
	return st.mode, nil
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
