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

// Reason is why custom agents' limits are hard or soft. Each value is one
// known case, so callers map it to their own words with a switch, never by
// reading the text. The text is what the start log line says.
type Reason string

// The reasons ProbeCgroups gives. ReasonWritable is the only hard one.
const (
	ReasonWritable       Reason = "cgroup v2, writable"
	ReasonNoCgroupV2     Reason = "no cgroup v2"
	ReasonNoCgroupMount  Reason = "no cgroup v2 at the cgroup mount"
	ReasonNotRoot        Reason = "the daemon does not run as root"
	ReasonHostRootCgroup Reason = "the daemon is in the host's root cgroup"
	ReasonNoCPU          Reason = "cgroup v2 has no cpu controller"
	ReasonNoMemory       Reason = "cgroup v2 has no memory controller"
	ReasonNoPids         Reason = "cgroup v2 has no pids controller"
	ReasonReadOnly       Reason = "cgroup tree is read-only"
	ReasonNotWritable    Reason = "cgroup tree is not writable"
)

// missingController is the reason for each controller in controllers.
var missingController = map[string]Reason{"cpu": ReasonNoCPU, "memory": ReasonNoMemory, "pids": ReasonNoPids}

// Advice says, in plain words for the people who run the machine, why
// custom agents' limits are soft and how to make them hard: why on the
// first line, then a "To fix:" list. It never restates the limits. It uses
// the contract's small reason format (newlines, "- " list lines, code in
// backticks). It is empty for ReasonWritable (hard) and for a reason it
// does not know.
func (r Reason) Advice() string {
	const (
		noCgroup = ", so the daemon cannot give each custom agent a cgroup of its own.\nTo fix:\n"
		compose  = "- Make sure compose.yaml has `cgroup: private` and `security_opt: writable-cgroups=true` for the machine service.\n"
		docker   = "- Use Docker Engine 28 or newer.\n"
		restart  = "- Restart the machine and check its log for `limits: hard`."
	)
	noController := func(name string) string {
		return "The machine's cgroup has no `" + name + "` controller" + noCgroup +
			"- Use Docker Engine 28 or newer on a host with cgroup v2.\n" + restart
	}
	switch r {
	case ReasonNoCgroupV2, ReasonNoCgroupMount:
		return "The host has no cgroup v2" + noCgroup +
			"- Run the stack on a Linux host with cgroup v2 (Docker Desktop, colima and current Linux servers have it).\n" + restart
	case ReasonNotRoot:
		return "The daemon does not run as root in the machine container" + noCgroup +
			"- Run the machine service as root (compose.yaml does).\n" +
			"- Keep `cgroup: private` and `security_opt: writable-cgroups=true` for the machine service.\n" +
			"- Use Docker Engine 28 or newer on a host with cgroup v2.\n" + restart
	case ReasonHostRootCgroup:
		return "The machine container shares the host's root cgroup, where the daemon never makes cgroups" + noCgroup +
			"- Make sure compose.yaml has `cgroup: private` for the machine service.\n" + restart
	case ReasonNoCPU:
		return noController("cpu")
	case ReasonNoMemory:
		return noController("memory")
	case ReasonNoPids:
		return noController("pids")
	case ReasonReadOnly:
		return "The machine container's cgroup tree is read-only" + noCgroup + compose + docker + restart
	case ReasonNotWritable:
		return "The daemon could not write the machine container's cgroup tree" + noCgroup + compose + docker + restart
	default:
		return ""
	}
}

// Boot is how custom agents' limits hold on this machine, decided once when
// the daemon starts.
type Boot struct {
	// Mode is LimitsHard when the daemon prepared its cgroup tree, so
	// every custom agent gets its own cgroup, else LimitsSoft.
	Mode domain.LimitMode
	// Root is the directory to use as Launcher.CgroupRoot, or "" when soft.
	Root string
	// Reason says why; its text is the start log line's.
	Reason Reason
	// Cause is the error behind a soft mode, when there is one.
	Cause error
}

// Line is the start log line, such as "limits: hard (cgroup v2, writable)".
func (b Boot) Line() string { return "limits: " + string(b.Mode) + " (" + string(b.Reason) + ")" }

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
		return Boot{Mode: domain.LimitsSoft, Reason: ReasonNoCgroupV2, Cause: err}
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
	soft := func(reason Reason, cause error) Boot {
		return Boot{Mode: domain.LimitsSoft, Reason: reason, Cause: cause}
	}
	own, ok := unifiedPath(self)
	if !ok {
		return soft(ReasonNoCgroupV2, nil)
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
		return soft(ReasonNoCgroupMount, err)
	}
	if !root {
		return soft(ReasonNotRoot, nil)
	}
	// cgroup.type exists in every cgroup but the host's root.
	if _, err := os.Stat(filepath.Join(dir, "cgroup.type")); err != nil { // #nosec G703 -- cgroupfs control file of the daemon's own cgroup
		return soft(ReasonHostRootCgroup, err)
	}
	have := strings.Fields(string(available))
	for _, c := range strings.Fields(strings.ReplaceAll(controllers, "+", "")) {
		if !slices.Contains(have, c) {
			return soft(missingController[c], nil)
		}
	}
	agents, err := PrepareCgroups(dir)
	if err != nil {
		return soft(prepareReason(err), err)
	}
	return Boot{Mode: domain.LimitsHard, Root: agents, Reason: ReasonWritable}
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
func prepareReason(err error) Reason {
	if errors.Is(err, syscall.EROFS) {
		return ReasonReadOnly
	}
	return ReasonNotWritable
}
