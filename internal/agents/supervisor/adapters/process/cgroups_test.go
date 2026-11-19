// SPDX-License-Identifier: MIT

package process_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/SDK-E/agentsone/internal/agents/supervisor/adapters/process"
)

// fakeCgroupFS lays out the control files PrepareCgroups touches. Real
// cgroupfs behaviour is proven by the system tests.
func fakeCgroupFS(t *testing.T) string {
	t.Helper()
	fs := t.TempDir()
	files := map[string]string{
		"cgroup.procs":                  "1\n42\n",
		"cgroup.subtree_control":        "",
		"daemon/cgroup.procs":           "",
		"agents/cgroup.subtree_control": "",
	}
	for name, body := range files {
		p := filepath.Join(fs, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return fs
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path) // #nosec G304 -- file under t.TempDir()
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestPrepareCgroups_MovesProcessesAndDelegatesControllers(t *testing.T) {
	t.Parallel()
	fs := fakeCgroupFS(t)

	root, err := process.PrepareCgroups(fs)
	if err != nil {
		t.Fatalf("PrepareCgroups() error = %v", err)
	}
	if root != filepath.Join(fs, "agents") {
		t.Errorf("root = %q, want %s/agents", root, fs)
	}
	// A regular file keeps the last write; cgroupfs takes each pid.
	if got := read(t, filepath.Join(fs, "daemon/cgroup.procs")); got != "42" {
		t.Errorf("daemon cgroup.procs = %q, want the last moved pid 42", got)
	}
	for _, f := range []string{"cgroup.subtree_control", "agents/cgroup.subtree_control"} {
		if got := read(t, filepath.Join(fs, f)); got != "+cpu +memory +pids" {
			t.Errorf("%s = %q", f, got)
		}
	}
}

func TestPrepareCgroups_ReportsReadOnlyTree(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		remove string
	}{
		{"no root process list", "cgroup.procs"},
		{"controllers cannot be delegated", "cgroup.subtree_control"},
		{"agents controllers cannot be delegated", "agents/cgroup.subtree_control"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			fs := fakeCgroupFS(t)
			if err := os.Remove(filepath.Join(fs, tt.remove)); err != nil {
				t.Fatal(err)
			}
			if _, err := process.PrepareCgroups(fs); !errors.Is(err, process.ErrCgroupsReadOnly) {
				t.Fatalf("PrepareCgroups() error = %v, want ErrCgroupsReadOnly", err)
			}
		})
	}
	t.Run("no cgroup tree at all", func(t *testing.T) {
		t.Parallel()
		if _, err := process.PrepareCgroups(filepath.Join(t.TempDir(), "missing")); !errors.Is(err, process.ErrCgroupsReadOnly) {
			t.Fatalf("PrepareCgroups() error = %v, want ErrCgroupsReadOnly", err)
		}
	})
}
