// SPDX-License-Identifier: MIT

package system

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/machine/app"
	"github.com/SDK-E/agentsone/internal/machine/domain"
)

// clockTicks is USER_HZ, the unit of /proc times; 100 on every Linux the
// machine image supports.
const clockTicks = 100

// SignalFunc delivers a signal to a process.
type SignalFunc func(pid int, s domain.Signal) error

// Procs reads the process table from a proc file system.
type Procs struct {
	root   string
	users  Passwd
	signal SignalFunc
}

var _ app.ProcessTable = Procs{}

// NewProcs reads processes under root (normally /proc).
func NewProcs(root string, users Passwd, signal SignalFunc) Procs {
	return Procs{root: root, users: users, signal: signal}
}

// List returns every process that could be read.
func (p Procs) List(ctx context.Context) ([]domain.Process, error) {
	entries, err := os.ReadDir(p.root)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", p.root, err)
	}
	boot, uptime := p.bootTime(), p.uptime()
	var out []domain.Process
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		if proc, ok := p.read(ctx, pid, boot, uptime); ok {
			out = append(out, proc)
		}
	}
	return out, nil
}

// Get returns one process.
func (p Procs) Get(ctx context.Context, pid int) (domain.Process, error) {
	proc, ok := p.read(ctx, pid, p.bootTime(), p.uptime())
	if !ok {
		return domain.Process{}, fmt.Errorf("pid %d: %w", pid, core.ErrNotFound)
	}
	return proc, nil
}

// Signal delivers s to pid.
func (p Procs) Signal(_ context.Context, pid int, s domain.Signal) error {
	return p.signal(pid, s)
}

func (p Procs) read(ctx context.Context, pid int, boot time.Time, uptime float64) (domain.Process, bool) {
	dir := filepath.Join(p.root, strconv.Itoa(pid))
	stat, err := os.ReadFile(filepath.Join(dir, "stat")) //nolint:gosec // G304: a numeric pid under the proc root.
	if err != nil {
		return domain.Process{}, false
	}
	// The command name sits in parentheses and may hold spaces; fields
	// after it are space separated, starting at field 3 (state).
	end := strings.LastIndexByte(string(stat), ')')
	if end < 0 {
		return domain.Process{}, false
	}
	f := strings.Fields(string(stat)[end+1:])
	if len(f) < 22 {
		return domain.Process{}, false
	}
	ppid, _ := strconv.Atoi(f[1])
	utime, _ := strconv.ParseFloat(f[11], 64)
	stime, _ := strconv.ParseFloat(f[12], 64)
	start, _ := strconv.ParseFloat(f[19], 64)
	rssPages, _ := strconv.ParseInt(f[21], 10, 64)
	uid := readUID(filepath.Join(dir, "status"))
	proc := domain.Process{
		PID: pid, PPID: ppid, UID: uid, User: p.users.nameOf(ctx, uid),
		Command:  readCmdline(filepath.Join(dir, "cmdline")),
		RSSBytes: rssPages * int64(os.Getpagesize()),
		Started:  boot.Add(time.Duration(start / clockTicks * float64(time.Second))).UTC(),
	}
	if alive := uptime - start/clockTicks; alive > 0 {
		proc.CPUPercent = min((utime+stime)/clockTicks/alive*100, 100)
	}
	if domain.KindOfUID(uid) == domain.UserAgent {
		proc.Agent = proc.User
	}
	return proc, true
}

func (p Procs) bootTime() time.Time {
	for _, line := range readLines(filepath.Join(p.root, "stat")) {
		if v, ok := strings.CutPrefix(line, "btime "); ok {
			n, _ := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
			return time.Unix(n, 0)
		}
	}
	return time.Unix(0, 0)
}

func (p Procs) uptime() float64 {
	b, _ := os.ReadFile(filepath.Join(p.root, "uptime"))
	f := strings.Fields(string(b))
	if len(f) == 0 {
		return 0
	}
	v, _ := strconv.ParseFloat(f[0], 64)
	return v
}

func readUID(statusPath string) int {
	for _, line := range readLines(statusPath) {
		if v, ok := strings.CutPrefix(line, "Uid:"); ok {
			if f := strings.Fields(v); len(f) > 0 {
				uid, _ := strconv.Atoi(f[0])
				return uid
			}
		}
	}
	return -1
}

func readCmdline(p string) string {
	b, _ := os.ReadFile(p) //nolint:gosec // G304: a numeric pid under the proc root.
	return strings.TrimSpace(strings.ReplaceAll(string(b), "\x00", " "))
}

func readLines(p string) []string {
	f, err := os.Open(p) //nolint:gosec // G304: fixed proc, cgroup and passwd paths set at wiring.
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()
	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		out = append(out, sc.Text())
	}
	return out
}
