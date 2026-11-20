// SPDX-License-Identifier: MIT

//go:build linux && integration

package boot_test

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/agents/supervisor/adapters/accounts"
	"github.com/SDK-E/agentsone/internal/agents/supervisor/adapters/process"
	"github.com/SDK-E/agentsone/internal/agents/supervisor/domain"
)

const (
	waitFor = 10 * time.Second
	// inContainer is set by the CI job that runs this binary in a container
	// of its own. Only there may the probe rearrange the cgroup tree; on a
	// host it would move the host's own processes.
	inContainer = "AGENTSONE_SYSTEST_IN_CONTAINER"
	// treeEnv is "read-only" when the CI job started the container without
	// writable-cgroups, so the cgroup tree is mounted read-only.
	treeEnv = "AGENTSONE_SYSTEST_CGROUPS"
)

var (
	boot    process.Boot
	bootLog string
	probed  bool
)

func TestMain(m *testing.M) {
	if os.Getenv(inContainer) == "1" {
		// The daemon's own start: agentsoned runs exactly this before any
		// agent, and wires boot.Root into its runner launcher.
		var buf bytes.Buffer
		boot = process.ProbeSystem()
		boot.Log(slog.New(slog.NewTextHandler(&buf, nil)))
		bootLog, probed = strings.TrimSpace(buf.String()), true
	}
	os.Exit(m.Run())
}

// The start probe gives custom agents hard limits on the machine recipe and
// soft ones on a read-only cgroup tree, and says which on one log line.
func TestBoot_LimitsFollowTheCgroupTree(t *testing.T) {
	if !probed {
		t.Fatalf("%s is not 1: run inside a container of its own (CI job go-integration)", inContainer)
	}
	if os.Geteuid() != 0 {
		t.Fatal("the boot system test needs root in its container (see the go-integration CI job)")
	}
	t.Logf("proof boot: daemon start log: %s", bootLog)
	if os.Getenv(treeEnv) == "read-only" {
		t.Run("read-only tree gives soft limits", softLimits)
		return
	}
	t.Run("machine recipe gives hard limits", hardLimits)
}

// hardLimits fails when the probe fell back to soft on the machine recipe:
// that would leave every custom agent without its cgroup.
func hardLimits(t *testing.T) {
	if boot.Mode != domain.LimitsHard {
		t.Fatalf("limits fell back to soft on the machine recipe: %s (cause: %v)", boot.Line(), boot.Cause)
	}
	if want := filepath.Join(process.CgroupFS, process.AgentsCgroup); boot.Root != want {
		t.Fatalf("cgroup root = %q, want %s", boot.Root, want)
	}
	if !strings.Contains(bootLog, `level=INFO msg="limits: hard (cgroup v2, writable)"`) {
		t.Errorf("start log = %q, want the info line limits: hard (cgroup v2, writable)", bootLog)
	}
	if got := ownCgroup(t); got != "0::/"+process.DaemonCgroup {
		t.Errorf("daemon cgroup after the probe = %q, want 0::/%s", got, process.DaemonCgroup)
	}
	t.Run("memory", func(t *testing.T) {
		acc := agentUser(t, "aoboot-mem", 200201)
		l, out := run(t, acc, domain.Limits{MemoryMaxBytes: 64 << 20, TasksMax: 64},
			`read -r cg < /proc/self/cgroup; tail /dev/zero; echo "$cg exit=$?" > "$OUT"; exec sleep 600`)
		got := readFifo(t, out)
		dir := filepath.Join(boot.Root, acc.User)
		kills := stat(t, dir, "memory.events", "oom_kill")
		t.Logf("proof boot memory: runner as uid 200201 reported %q; memory.max=%s oom_kill=%d mode=%s",
			got, value(t, dir, "memory.max"), kills, l.Mode(acc.User))
		if want := "0::/" + process.AgentsCgroup + "/aoboot-mem exit=137"; got != want {
			t.Errorf("runner reported %q, want %q (in its cgroup, allocation killed)", got, want)
		}
		if v := value(t, dir, "memory.max"); v != strconv.Itoa(64<<20) {
			t.Errorf("memory.max = %s, want %d", v, 64<<20)
		}
		if kills < 1 || l.Mode(acc.User) != domain.LimitsHard {
			t.Errorf("oom_kill = %d, mode = %s; want at least one kernel OOM kill and hard", kills, l.Mode(acc.User))
		}
	})
	t.Run("pids", func(t *testing.T) {
		acc := agentUser(t, "aoboot-pid", 200202)
		// The shell exits when a fork fails, so the forks run in a child shell.
		l, out := run(t, acc, domain.Limits{TasksMax: 20},
			`sh -c 'i=0; while [ "$i" -lt 100 ]; do sleep 60 & i=$((i+1)); done' 2>/dev/null; echo "forks exit=$?" > "$OUT"; exec sleep 600`)
		got := readFifo(t, out)
		dir := filepath.Join(boot.Root, acc.User)
		current, _ := strconv.Atoi(value(t, dir, "pids.current"))
		refused := stat(t, dir, "pids.events", "max")
		t.Logf("proof boot pids: 100x `sleep 60 &` as uid 200202 reported %q; pids.max=%s pids.current=%d refused forks=%d mode=%s",
			got, value(t, dir, "pids.max"), current, refused, l.Mode(acc.User))
		if v := value(t, dir, "pids.max"); v != "20" {
			t.Errorf("pids.max = %s, want 20", v)
		}
		if current > 20 || refused < 1 || got == "forks exit=0" || l.Mode(acc.User) != domain.LimitsHard {
			t.Errorf("pids.current=%d refused=%d %s mode=%s; want at most 20, refused forks, a failed fork and hard",
				current, refused, got, l.Mode(acc.User))
		}
	})
}

// softLimits: without writable cgroups the daemon still runs custom agents,
// as their own users, with process resource limits only.
func softLimits(t *testing.T) {
	if boot.Mode != domain.LimitsSoft || boot.Root != "" {
		t.Fatalf("probe = %s root %q; want soft with no cgroup root on a read-only tree", boot.Line(), boot.Root)
	}
	if !strings.Contains(bootLog, `level=INFO msg="limits: soft (cgroup tree is read-only)"`) {
		t.Errorf("start log = %q, want the info line limits: soft (cgroup tree is read-only)", bootLog)
	}
	acc := agentUser(t, "aoboot-soft", 200203)
	l, out := run(t, acc, domain.Limits{MemoryMaxBytes: 64 << 20, TasksMax: 64},
		`read -r cg < /proc/self/cgroup; while read -r k v rest; do [ "$k" = Uid: ] && uid=$v; done < /proc/self/status; `+
			`while read -r a b c d rest; do [ "$a $b $c" = "Max address space" ] && as=$d; done < /proc/self/limits; `+
			`echo "$cg uid=$uid as=$as" > "$OUT"; exec sleep 600`)
	got := readFifo(t, out)
	t.Logf("proof boot soft: runner reported %q mode=%s", got, l.Mode(acc.User))
	if want := fmt.Sprintf("0::/ uid=200203 as=%d", 64<<20); got != want || l.Mode(acc.User) != domain.LimitsSoft {
		t.Errorf("runner reported %q mode %s; want %q and soft", got, l.Mode(acc.User), want)
	}
}

func ownCgroup(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(b))
}

// agentUser creates a real custom agent user with a fixed uid and removes
// it, with its home, when the test ends.
func agentUser(t *testing.T, name string, uid int) domain.Account {
	t.Helper()
	acc := domain.AccountFor(name, false).WithUID(uid)
	t.Cleanup(func() {
		// The container is thrown away after the run, so a user whose last
		// process is still ending may stay.
		_ = exec.Command("userdel", name).Run() // #nosec G204 -- fixed test user
		if err := os.RemoveAll(acc.Home); err != nil {
			t.Errorf("remove %s: %v", acc.Home, err)
		}
	})
	if _, err := (&accounts.Accounts{}).Ensure(t.Context(), acc); err != nil {
		t.Fatalf("Ensure(%s) error = %v", name, err)
	}
	return acc
}

// run starts a shell runner for acc on the launcher the daemon wires (its
// cgroup root from the probe) and stops it when the test ends.
func run(t *testing.T, acc domain.Account, limits domain.Limits, script string) (*process.Launcher, string) {
	t.Helper()
	out := fifo(t)
	l := &process.Launcher{CgroupRoot: boot.Root}
	spec := domain.UnitSpec{Agent: acc.User, Account: acc, Limits: limits,
		Exec: domain.Exec{Path: "/bin/sh", Args: []string{"-c", script}, Env: []string{"OUT=" + out}}}
	if err := l.Install(t.Context(), spec); err != nil {
		t.Fatal(err)
	}
	if err := l.Start(t.Context(), acc.User); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := l.Stop(context.Background(), acc.User); err != nil {
			t.Errorf("stop %s: %v", acc.User, err)
		}
	})
	return l, out
}

// fifo is a named pipe any agent user may write to.
func fifo(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "aoboot")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Errorf("remove %s: %v", dir, err)
		}
	})
	if err := os.Chmod(dir, 0o755); err != nil { // #nosec G302 -- agent users must reach the FIFO
		t.Fatal(err)
	}
	path := filepath.Join(dir, "out")
	if err := syscall.Mkfifo(path, 0o666); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o666); err != nil { // #nosec G302 -- agent users write to it
		t.Fatal(err)
	}
	return path
}

func readFifo(t *testing.T, path string) string {
	t.Helper()
	got := make(chan string, 1)
	go func() {
		b, _ := os.ReadFile(path) // #nosec G304 -- test FIFO; an error reads as no output
		got <- strings.TrimSpace(string(b))
	}()
	select {
	case s := <-got:
		return s
	case <-time.After(waitFor):
		// Open the write end so the reader sees EOF and the goroutine ends.
		if f, err := os.OpenFile(path, os.O_WRONLY, 0); err == nil { // #nosec G304 -- test FIFO
			if err := f.Close(); err != nil {
				t.Error(err)
			}
		}
		<-got
		t.Fatal("runner wrote nothing within 10s")
		return ""
	}
}

// stat reads one key of a flat-keyed cgroup file such as memory.events.
func stat(t *testing.T, dir, file, key string) int64 {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, file)) // #nosec G304 -- cgroupfs file
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(b), "\n") {
		if k, v, ok := strings.Cut(line, " "); ok && k == key {
			n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
			if err != nil {
				t.Fatalf("%s %s = %q: %v", file, key, v, err)
			}
			return n
		}
	}
	t.Fatalf("%s has no %s:\n%s", file, key, b)
	return 0
}

func value(t *testing.T, dir, file string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, file)) // #nosec G304 -- cgroupfs file
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(b))
}
