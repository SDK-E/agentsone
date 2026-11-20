// SPDX-License-Identifier: MIT

package system

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// MarkerVar names, in every program of a desktop, the desktop's runtime
// folder. labwc starts programs in their own session, so they leave the
// compositor's process group; the marker finds them at stop time without
// touching the human's other programs (their IDE server runs as the same
// user).
const MarkerVar = "AGENTSONE_DESKTOP"

// Procs finds a desktop's leftover programs in the process table.
type Procs struct {
	// Root is the process table, /proc in production.
	Root string
	// Kill signals a process; nil is syscall.Kill.
	Kill func(pid int, sig syscall.Signal) error
}

// Sweep signals every process of uid whose environment carries the marker
// of the desktop at runtime. Processes that vanish while it reads are
// skipped.
func (p Procs) Sweep(uid int, runtime string, sig syscall.Signal) {
	entries, err := os.ReadDir(p.Root)
	if err != nil {
		return
	}
	kill := p.Kill
	if kill == nil {
		kill = syscall.Kill
	}
	marker := []byte(MarkerVar + "=" + runtime)
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || !p.owned(pid, uid) {
			continue
		}
		env, err := os.ReadFile(filepath.Join(p.Root, e.Name(), "environ"))
		if err != nil {
			continue
		}
		for kv := range bytes.SplitSeq(env, []byte{0}) {
			if bytes.Equal(kv, marker) {
				_ = kill(pid, sig) // it may have ended since; nothing else to do
				break
			}
		}
	}
}

// owned reports whether pid's real uid is uid.
func (p Procs) owned(pid, uid int) bool {
	f, err := os.Open(filepath.Join(p.Root, strconv.Itoa(pid), "status"))
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	s := bufio.NewScanner(f)
	for s.Scan() {
		if rest, ok := strings.CutPrefix(s.Text(), "Uid:"); ok {
			fields := strings.Fields(rest)
			return len(fields) > 0 && fields[0] == strconv.Itoa(uid)
		}
	}
	return false
}
