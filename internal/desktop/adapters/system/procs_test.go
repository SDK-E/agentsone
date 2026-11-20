// SPDX-License-Identifier: MIT

package system_test

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/SDK-E/agentsone/internal/desktop/adapters/system"
)

// proc writes /proc/<pid>/status and environ in root.
func proc(t *testing.T, root, pid string, uid int, env ...string) {
	t.Helper()
	dir := filepath.Join(root, pid)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	status := "Name:\tfoot\nUid:\t" + itoa(uid) + "\t" + itoa(uid) + "\t" + itoa(uid) + "\t" + itoa(uid) + "\n"
	if err := os.WriteFile(filepath.Join(dir, "status"), []byte(status), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "environ"), []byte(strings.Join(env, "\x00")+"\x00"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

func TestSweepSignalsOnlyTheDesktopsOwnLeftovers(t *testing.T) {
	root := t.TempDir()
	marker := "AGENTSONE_DESKTOP=/run/agentsone/desktop/1"
	proc(t, root, "10", 1, "HOME=/home/ada", marker)                                // chromium of the desktop
	proc(t, root, "11", 1, "HOME=/home/ada")                                        // the human's IDE server
	proc(t, root, "12", 2, marker)                                                  // another user with the marker
	proc(t, root, "13", 1, "AGENTSONE_DESKTOP=/run/agentsone/desktop/10")           // another desktop
	proc(t, root, "14", 1, "X=1", "AGENTSONE_DESKTOP=/run/agentsone/desktop/1", "") // marker not first
	if err := os.MkdirAll(filepath.Join(root, "self"), 0o700); err != nil {         // not a pid
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "15"), 0o700); err != nil { // gone between list and read
		t.Fatal(err)
	}
	var hit []int
	p := system.Procs{Root: root, Kill: func(pid int, sig syscall.Signal) error {
		if sig != syscall.SIGKILL {
			t.Errorf("signal %v", sig)
		}
		hit = append(hit, pid)
		return nil
	}}
	p.Sweep(1, "/run/agentsone/desktop/1", syscall.SIGKILL)
	slices.Sort(hit)
	if !slices.Equal(hit, []int{10, 14}) {
		t.Errorf("signalled %v, want [10 14]", hit)
	}
}

func TestSweepOfAMissingProcessTableDoesNothing(t *testing.T) {
	p := system.Procs{Root: filepath.Join(t.TempDir(), "none"), Kill: func(int, syscall.Signal) error {
		t.Error("signalled a process")
		return nil
	}}
	p.Sweep(1, "/x", syscall.SIGKILL)
}
