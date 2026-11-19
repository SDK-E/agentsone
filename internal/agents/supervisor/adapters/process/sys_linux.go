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
	cgroup string   // the agent's cgroup directory, or ""
	fd     *os.File // open cgroup directory, passed to clone
	isRoot bool
}

// prepare sets the runner's credentials and, when the cgroup tree is
// writable, the per-agent cgroup the child is cloned into (clone3 with
// CLONE_INTO_CGROUP), so there is no moment without limits.
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
	fd, err := os.Open(dir) // #nosec G304 -- our own cgroup directory
	if err != nil {
		_ = os.Remove(dir)
		return st, nil
	}
	st.cgroup, st.fd = dir, fd
	cmd.SysProcAttr.UseCgroupFD = true
	cmd.SysProcAttr.CgroupFD = int(fd.Fd())
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

// afterStart sets process resource limits on a custom agent's runner. With
// a cgroup they back it up; without one they are the only limits (soft).
func (st *sysState) afterStart(pid int, l domain.Limits) domain.LimitMode {
	if st.fd != nil {
		_ = st.fd.Close()
		st.fd = nil
	}
	if st.isRoot {
		return domain.LimitsNone
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
		return domain.LimitsNone
	case st.cgroup != "":
		return domain.LimitsHard
	default:
		return domain.LimitsSoft
	}
}

// cleanup removes the agent's cgroup once its processes are gone.
func (st *sysState) cleanup() {
	if st.fd != nil {
		_ = st.fd.Close()
		st.fd = nil
	}
	if st.cgroup != "" {
		_ = os.Remove(st.cgroup)
	}
}
