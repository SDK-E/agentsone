// SPDX-License-Identifier: MIT

package process

import (
	"bufio"
	"bytes"
	"errors"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/SDK-E/agentsone/internal/agents/supervisor/domain"
)

// selfCgroup is where a process reads its own cgroup.
const selfCgroup = "/proc/self/cgroup"

// Boot is how custom agents' limits hold on this machine, decided once when
// the daemon starts.
type Boot struct {
	// Mode is LimitsHard when the daemon prepared its cgroup tree, so
	// every custom agent gets its own cgroup, else LimitsSoft.
	Mode domain.LimitMode
	// Root is the directory to use as Launcher.CgroupRoot, or "" when soft.
	Root string
	// Reason says why, in plain words, for the start log line.
	Reason string
	// Cause is the error behind a soft mode, when there is one.
	Cause error
}

// Line is the start log line, such as "limits: hard (cgroup v2, writable)".
func (b Boot) Line() string { return "limits: " + string(b.Mode) + " (" + b.Reason + ")" }

// Log writes the start log line at info level: soft limits are a supported
// way to run, so they are not an error.
func (b Boot) Log(log *slog.Logger) {
	switch {
	case b.Cause != nil:
		log.Info(b.Line(), "cause", b.Cause.Error())
	case b.Root != "":
		log.Info(b.Line(), "cgroup", b.Root)
	default:
		log.Info(b.Line())
	}
}

// ProbeSystem is ProbeCgroups on this machine: cgroup v2 at CgroupFS, the
// daemon's own cgroup from /proc/self/cgroup, root from the effective uid.
func ProbeSystem() Boot {
	self, err := os.ReadFile(selfCgroup)
	if err != nil {
		return Boot{Mode: domain.LimitsSoft, Reason: "no cgroup v2", Cause: err}
	}
	return ProbeCgroups(CgroupFS, self, os.Geteuid() == 0)
}

// ProbeCgroups decides how custom agents' limits hold and, when they can be
// hard, prepares the tree with PrepareCgroups. It works on the daemon's own
// cgroup (self is its /proc/self/cgroup, under the cgroup v2 mount fs),
// never above it: in the machine container that is the container's cgroup
// namespace root. Hard limits need cgroup v2, root, a cgroup of the
// daemon's own (never the host's root cgroup, whose processes are the
// host's), the cpu, memory and pids controllers and a writable tree. Any
// other case is soft: agents run with process resource limits only.
func ProbeCgroups(fs string, self []byte, root bool) Boot {
	soft := func(reason string, cause error) Boot {
		return Boot{Mode: domain.LimitsSoft, Reason: reason, Cause: cause}
	}
	own, ok := unifiedPath(self)
	if !ok {
		return soft("no cgroup v2", nil)
	}
	// A daemon started again in the tree it made sits in /daemon; the
	// tree it prepares is the one above.
	if path.Base(own) == DaemonCgroup {
		own = path.Dir(own)
	}
	// own is the kernel's absolute, cleaned path of the daemon's cgroup, so
	// dir stays under fs.
	dir := filepath.Join(fs, filepath.FromSlash(own))
	available, err := os.ReadFile(filepath.Join(dir, "cgroup.controllers")) // #nosec G304 G703 -- cgroupfs control file of the daemon's own cgroup
	if err != nil {
		return soft("no cgroup v2 at the cgroup mount", err)
	}
	if !root {
		return soft("the daemon does not run as root", nil)
	}
	// cgroup.type exists in every cgroup but the host's root.
	if _, err := os.Stat(filepath.Join(dir, "cgroup.type")); err != nil { // #nosec G703 -- cgroupfs control file of the daemon's own cgroup
		return soft("the daemon is in the host's root cgroup", err)
	}
	have := strings.Fields(string(available))
	for _, c := range strings.Fields(strings.ReplaceAll(controllers, "+", "")) {
		if !slices.Contains(have, c) {
			return soft("cgroup v2 has no "+c+" controller", nil)
		}
	}
	agents, err := PrepareCgroups(dir)
	if err != nil {
		return soft(prepareReason(err), err)
	}
	return Boot{Mode: domain.LimitsHard, Root: agents, Reason: "cgroup v2, writable"}
}

// unifiedPath is the cgroup v2 path in a /proc/<pid>/cgroup file: its
// "0::<path>" line.
func unifiedPath(self []byte) (string, bool) {
	sc := bufio.NewScanner(bytes.NewReader(self))
	for sc.Scan() {
		if p, ok := strings.CutPrefix(sc.Text(), "0::"); ok && strings.HasPrefix(p, "/") {
			return path.Clean(p), true
		}
	}
	return "", false
}

// prepareReason names why PrepareCgroups failed: a read-only mount, as
// Docker gives without writable-cgroups, or any other refusal.
func prepareReason(err error) string {
	if errors.Is(err, syscall.EROFS) {
		return "cgroup tree is read-only"
	}
	return "cgroup tree is not writable"
}
