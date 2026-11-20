// SPDX-License-Identifier: MIT

package process_test

import (
	"bytes"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SDK-E/agentsone/internal/agents/supervisor/adapters/process"
	"github.com/SDK-E/agentsone/internal/agents/supervisor/domain"
)

// probeFS is a fake cgroup v2 namespace root that the daemon may write: the
// files ProbeCgroups reads plus the ones PrepareCgroups touches.
func probeFS(t *testing.T) string {
	t.Helper()
	fs := fakeCgroupFS(t)
	for name, body := range map[string]string{
		"cgroup.controllers": "cpuset cpu io memory hugetlb pids rdma misc\n",
		"cgroup.type":        "domain\n",
	} {
		if err := os.WriteFile(filepath.Join(fs, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return fs
}

// inRoot is /proc/self/cgroup of a process at its namespace's root, as the
// daemon is when the machine container starts with `cgroup: private`.
var inRoot = []byte("0::/\n")

func TestProbeCgroups_WritableTreeGivesHardLimits(t *testing.T) {
	t.Parallel()
	fs := probeFS(t)

	boot := process.ProbeCgroups(fs, inRoot, true)

	if boot.Mode != domain.LimitsHard || boot.Root != filepath.Join(fs, process.AgentsCgroup) || boot.Cause != nil {
		t.Fatalf("ProbeCgroups() = %+v, want hard at %s/agents", boot, fs)
	}
	if got := boot.Line(); got != "limits: hard (cgroup v2, writable)" {
		t.Errorf("Line() = %q", got)
	}
	// The tree is prepared: the daemon left the root for /daemon and the
	// agents' parent delegates the controllers.
	if got := read(t, filepath.Join(fs, "daemon/cgroup.procs")); got != "42" {
		t.Errorf("daemon cgroup.procs = %q, want the moved pids", got)
	}
	if got := read(t, filepath.Join(fs, "agents/cgroup.subtree_control")); got != "+cpu +memory +pids" {
		t.Errorf("agents cgroup.subtree_control = %q", got)
	}
}

func TestProbeCgroups_UsesTheDaemonsOwnCgroup(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		self string
		dir  string // the subtree the daemon prepares, under fs
	}{
		// A host service with its own delegated cgroup prepares only that.
		{"delegated service", "0::/system.slice/agentsoned.service\n", "system.slice/agentsoned.service"},
		// A daemon started again in the tree it made finds that tree.
		{"started again", "0::/daemon\n", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			fs := t.TempDir()
			sub := probeFS(t)
			if err := os.MkdirAll(filepath.Dir(filepath.Join(fs, tt.dir)), 0o750); err != nil {
				t.Fatal(err)
			}
			if tt.dir == "" {
				fs = sub
			} else if err := os.Rename(sub, filepath.Join(fs, tt.dir)); err != nil {
				t.Fatal(err)
			}

			boot := process.ProbeCgroups(fs, []byte(tt.self), true)

			if want := filepath.Join(fs, tt.dir, process.AgentsCgroup); boot.Mode != domain.LimitsHard || boot.Root != want {
				t.Fatalf("ProbeCgroups() = %+v, want hard at %s", boot, want)
			}
		})
	}
}

func TestProbeCgroups_FallsBackToSoftLimitsAndSaysWhy(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		self   string
		root   bool
		change func(t *testing.T, fs string)
		line   string
		// moved: the probe got as far as moving the root's processes.
		moved bool
	}{
		{"cgroup v1 only", "12:pids:/\n1:name=systemd:/\n", true, nil,
			"limits: soft (no cgroup v2)", false},
		{"no cgroup v2 mount", "0::/\n", true, remove("cgroup.controllers"),
			"limits: soft (no cgroup v2 at the cgroup mount)", false},
		{"not root", "0::/\n", false, nil,
			"limits: soft (the daemon does not run as root)", false},
		{"host root cgroup", "0::/\n", true, remove("cgroup.type"),
			"limits: soft (the daemon is in the host's root cgroup)", false},
		{"missing controller", "0::/\n", true, write("cgroup.controllers", "cpu memory\n"),
			"limits: soft (cgroup v2 has no pids controller)", false},
		{"tree not writable", "0::/\n", true, remove("agents/cgroup.subtree_control"),
			"limits: soft (cgroup tree is not writable)", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			fs := probeFS(t)
			if tt.change != nil {
				tt.change(t, fs)
			}

			boot := process.ProbeCgroups(fs, []byte(tt.self), tt.root)

			if boot.Mode != domain.LimitsSoft || boot.Root != "" {
				t.Fatalf("ProbeCgroups() = %+v, want soft with no cgroup root", boot)
			}
			if got := boot.Line(); got != tt.line {
				t.Errorf("Line() = %q, want %q", got, tt.line)
			}
			if moved := read(t, filepath.Join(fs, "daemon/cgroup.procs")) != ""; moved != tt.moved {
				t.Errorf("processes moved = %v, want %v", moved, tt.moved)
			}
		})
	}
}

// The start log line is info, not an error: soft limits are a supported
// way to run, and the line says why.
func TestBoot_LogsOneInfoLine(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	cause := errors.New("create daemon cgroup: read-only file system")

	process.Boot{Mode: domain.LimitsSoft, Reason: "cgroup tree is read-only", Cause: cause}.Log(log)
	process.Boot{Mode: domain.LimitsHard, Reason: "cgroup v2, writable", Root: "/sys/fs/cgroup/agents"}.Log(log)

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("log = %q, want two lines", buf.String())
	}
	for i, want := range []string{
		`level=INFO msg="limits: soft (cgroup tree is read-only)" cause="create daemon cgroup: read-only file system"`,
		`level=INFO msg="limits: hard (cgroup v2, writable)" cgroup=/sys/fs/cgroup/agents`,
	} {
		if !strings.HasSuffix(lines[i], want) {
			t.Errorf("line %d = %q, want suffix %q", i, lines[i], want)
		}
	}
}

// Every soft reason has plain words for people, short enough for the API
// (1 to 500 characters): why on the first line, then a "To fix:" list in
// the contract's reason format, never the limits themselves. Hard has none.
func TestReasonAdvice(t *testing.T) {
	t.Parallel()
	soft := []process.Reason{
		process.ReasonNoCgroupV2, process.ReasonNoCgroupMount, process.ReasonNotRoot,
		process.ReasonHostRootCgroup, process.ReasonNoCPU, process.ReasonNoMemory,
		process.ReasonNoPids, process.ReasonReadOnly, process.ReasonNotWritable,
	}
	seen := map[string]process.Reason{}
	for _, r := range soft {
		a := r.Advice()
		if n := len([]rune(a)); n < 1 || n > 500 {
			t.Errorf("%q: advice of %d characters, want 1 to 500", r, n)
		}
		lines := strings.Split(a, "\n")
		if len(lines) < 3 || strings.HasPrefix(lines[0], "- ") || lines[1] != "To fix:" {
			t.Errorf("%q: advice %q is not why, then \"To fix:\" and a list", r, a)
		}
		for _, l := range lines[2:] {
			if !strings.HasPrefix(l, "- ") || strings.Count(l, "`")%2 != 0 {
				t.Errorf("%q: fix line %q is not a list item with whole code spans", r, l)
			}
		}
		if lines[len(lines)-1] != "- Restart the machine and check its log for `limits: hard`." {
			t.Errorf("%q: advice %q does not end with the restart", r, a)
		}
		for _, restated := range []string{"capped", "process resource limits", "does not hold"} {
			if strings.Contains(a, restated) {
				t.Errorf("%q: advice restates the limits (%q)", r, restated)
			}
		}
		if other, dup := seen[a]; dup && !sameCase(r, other) {
			t.Errorf("%q and %q share advice", r, other)
		}
		seen[a] = r
	}
	if a := process.ReasonWritable.Advice(); a != "" {
		t.Errorf("hard limits have advice %q", a)
	}
	if a := process.Reason("something new").Advice(); a != "" {
		t.Errorf("an unknown reason has advice %q", a)
	}
}

// sameCase is true for reasons that are one case for people: no cgroup v2
// at all, or none at the mount.
func sameCase(a, b process.Reason) bool {
	pair := map[process.Reason]bool{process.ReasonNoCgroupV2: true, process.ReasonNoCgroupMount: true}
	return pair[a] && pair[b]
}

func remove(name string) func(*testing.T, string) {
	return func(t *testing.T, fs string) {
		t.Helper()
		if err := os.Remove(filepath.Join(fs, name)); err != nil {
			t.Fatal(err)
		}
	}
}

func write(name, body string) func(*testing.T, string) {
	return func(t *testing.T, fs string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(fs, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}
