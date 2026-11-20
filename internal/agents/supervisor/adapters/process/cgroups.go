// SPDX-License-Identifier: MIT

package process

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// Cgroup layout inside the container's cgroup namespace.
const (
	// CgroupFS is where cgroup v2 is mounted.
	CgroupFS = "/sys/fs/cgroup"
	// DaemonCgroup holds the daemon and every process that was in the
	// namespace root, so the root may delegate controllers (cgroup v2 allows
	// no processes in a cgroup that has controllers enabled for children).
	DaemonCgroup = "daemon"
	// AgentsCgroup is the parent of one cgroup per custom agent.
	AgentsCgroup = "agents"
	// controllers are the ones agent cgroups need.
	controllers = "+cpu +memory +pids"
)

// ErrCgroupsReadOnly means the cgroup tree cannot be written; agents then
// get soft limits.
var ErrCgroupsReadOnly = errors.New("cgroup tree is not writable")

// PrepareCgroups sets up the layout at boot: every process in fs (the
// namespace root) moves to fs/daemon, then cpu, memory and pids are enabled
// for fs's children and for fs/agents' children. It returns the directory
// to use as Launcher.CgroupRoot. Any failure is ErrCgroupsReadOnly with the
// cause, and the daemon runs agents with soft limits.
func PrepareCgroups(fs string) (string, error) {
	fail := func(step string, err error) (string, error) {
		return "", fmt.Errorf("%w: %s: %w", ErrCgroupsReadOnly, step, err)
	}
	daemon := filepath.Join(fs, DaemonCgroup)
	if err := os.Mkdir(daemon, 0o755); err != nil && !os.IsExist(err) { // #nosec G301 G703 -- cgroupfs directory under the daemon's own cgroup
		return fail("create daemon cgroup", err)
	}
	procs, err := os.ReadFile(filepath.Join(fs, "cgroup.procs")) // #nosec G304 G703 -- cgroupfs control file of the daemon's own cgroup
	if err != nil {
		return fail("read root processes", err)
	}
	sc := bufio.NewScanner(bytes.NewReader(procs))
	for sc.Scan() {
		// One pid per write, as the kernel requires. A process that ended
		// in between is not an error.
		if err := writeControl(filepath.Join(daemon, "cgroup.procs"), sc.Text()); err != nil && !errors.Is(err, os.ErrNotExist) && !errors.Is(err, syscall.ESRCH) {
			return fail("move process "+sc.Text(), err)
		}
	}
	if err := writeControl(filepath.Join(fs, "cgroup.subtree_control"), controllers); err != nil {
		return fail("enable controllers at the root", err)
	}
	agents := filepath.Join(fs, AgentsCgroup)
	if err := os.Mkdir(agents, 0o755); err != nil && !os.IsExist(err) { // #nosec G301 G703 -- cgroupfs directory under the daemon's own cgroup
		return fail("create agents cgroup", err)
	}
	if err := writeControl(filepath.Join(agents, "cgroup.subtree_control"), controllers); err != nil {
		return fail("enable controllers for agents", err)
	}
	return agents, nil
}

// writeControl writes one value to an existing cgroup control file.
func writeControl(path, value string) error {
	f, err := os.OpenFile(path, os.O_WRONLY, 0) // #nosec G304 G703 -- cgroupfs control file under the daemon's own cgroup
	if err != nil {
		return err
	}
	_, werr := f.WriteString(value)
	return errors.Join(werr, f.Close())
}
