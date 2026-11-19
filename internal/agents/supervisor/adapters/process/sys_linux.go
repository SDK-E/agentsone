// SPDX-License-Identifier: MIT

//go:build linux

package process

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"syscall"

	"golang.org/x/sys/unix"

	"github.com/SDK-E/agentsone/internal/agents/supervisor/domain"
)

// sysState is what prepare set up for one process.
type sysState struct {
	cgroup  string // the agent's cgroup directory, or ""
	isRoot  bool
	stopped bool // the child stops at exec until afterStart releases it
}

// prepare sets a custom agent's credentials and makes its runner stop right
// after exec, before its first instruction (PTRACE_TRACEME). afterStart then
// moves it into its cgroup and sets its rlimits, and only then lets it run,
// so it never runs unlimited. clone3 with CLONE_INTO_CGROUP would do the
// same, but Docker's default seccomp profile answers clone3 with ENOSYS.
func prepare(cmd *exec.Cmd, spec domain.UnitSpec, cgroupRoot string) (*sysState, error) {
	st := &sysState{isRoot: spec.Account.IsRoot}
	if spec.Account.IsRoot {
		return st, nil
	}
	if os.Geteuid() == 0 {
		uid, gid, err := ids(spec.Account)
		if err != nil {
			return nil, err
		}
		// Groups is set so the runner keeps none of the daemon's groups.
		cmd.SysProcAttr.Credential = &syscall.Credential{Uid: uid, Gid: gid, Groups: []uint32{gid}}
	}
	cmd.SysProcAttr.Ptrace = true
	st.stopped = true
	if cgroupRoot == "" || spec.Limits == (domain.Limits{}) {
		return st, nil
	}
	dir := filepath.Join(cgroupRoot, spec.Agent)
	if err := os.Mkdir(dir, 0o755); err != nil && !os.IsExist(err) { // #nosec G301 -- cgroupfs directory
		return st, nil // not writable: soft limits
	}
	if err := writeLimits(dir, spec.Limits); err != nil {
		_ = os.Remove(dir)
		return st, nil
	}
	st.cgroup = dir
	return st, nil
}

func ids(a domain.Account) (uint32, uint32, error) {
	u, err := user.Lookup(a.User)
	if err != nil {
		return 0, 0, fmt.Errorf("look up user %s: %w", a.User, err)
	}
	uid, err := strconv.ParseUint(u.Uid, 10, 32)
	if err != nil {
		return 0, 0, fmt.Errorf("uid of %s: %w", a.User, err)
	}
	gid, err := strconv.ParseUint(u.Gid, 10, 32)
	if err != nil {
		return 0, 0, fmt.Errorf("gid of %s: %w", a.User, err)
	}
	if uid == 0 {
		return 0, 0, fmt.Errorf("custom agent user %s has uid 0", a.User)
	}
	return uint32(uid), uint32(gid), nil
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

// afterStart runs while a custom agent's runner is stopped at exec: it
// moves the runner into its cgroup, sets its rlimits and releases it. It
// must run on the OS thread that started the child (the tracer). With a
// cgroup the limits are hard; with rlimits only they are soft.
func (st *sysState) afterStart(pid int, l domain.Limits) (domain.LimitMode, error) {
	if st.isRoot {
		return domain.LimitsNone, nil
	}
	if st.stopped {
		var ws syscall.WaitStatus
		if _, err := syscall.Wait4(pid, &ws, syscall.WALL|syscall.WUNTRACED, nil); err != nil {
			return "", fmt.Errorf("wait for runner stop: %w", err)
		}
		defer func() { _ = syscall.PtraceDetach(pid) }()
	}
	if st.cgroup != "" {
		if err := os.WriteFile(filepath.Join(st.cgroup, "cgroup.procs"), []byte(strconv.Itoa(pid)), 0o644); err != nil { // #nosec G306 -- cgroupfs control file
			_ = syscall.Kill(pid, syscall.SIGKILL)
			return "", fmt.Errorf("move runner into %s: %w", st.cgroup, err)
		}
	}
	set := func(res int, v uint64) { _ = unix.Prlimit(pid, res, &unix.Rlimit{Cur: v, Max: v}, nil) }
	set(unix.RLIMIT_CORE, 0)
	set(unix.RLIMIT_NOFILE, DefaultNoFile)
	if l.TasksMax > 0 {
		set(unix.RLIMIT_NPROC, uint64(l.TasksMax))
	}
	if st.cgroup == "" && l.MemoryMaxBytes > 0 {
		set(unix.RLIMIT_AS, uint64(l.MemoryMaxBytes))
	}
	switch {
	case l == (domain.Limits{}):
		return domain.LimitsNone, nil
	case st.cgroup != "":
		return domain.LimitsHard, nil
	default:
		return domain.LimitsSoft, nil
	}
}

// cleanup removes the agent's cgroup once its processes are gone.
func (st *sysState) cleanup() {
	if st.cgroup != "" {
		_ = os.Remove(st.cgroup)
	}
}
