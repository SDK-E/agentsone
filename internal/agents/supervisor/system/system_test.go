// SPDX-License-Identifier: MIT

//go:build linux && integration

package system_test

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/agents/supervisor/adapters/accounts"
	"github.com/SDK-E/agentsone/internal/agents/supervisor/adapters/process"
	"github.com/SDK-E/agentsone/internal/agents/supervisor/adapters/socket"
	"github.com/SDK-E/agentsone/internal/agents/supervisor/domain"
)

const (
	waitFor = 10 * time.Second
	// inContainer is set by the CI job that runs this binary in an
	// unprivileged container. Only there may the tests rearrange the
	// cgroup tree; on a host it would move the host's own processes.
	inContainer = "AGENTSONE_SYSTEST_IN_CONTAINER"
)

var (
	cgroupRoot string
	cgroupErr  error
)

func TestMain(m *testing.M) {
	if os.Getenv(inContainer) == "1" {
		cgroupRoot, cgroupErr = process.PrepareCgroups(process.CgroupFS)
	} else {
		cgroupErr = fmt.Errorf("%s is not 1: run inside the unprivileged container (CI job go-integration)", inContainer)
	}
	os.Exit(m.Run())
}

// requireRoot fails, never skips, without root (B6d).
func requireRoot(t *testing.T) {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Fatal("system tests need root in their container (see the go-integration CI job)")
	}
}

func requireCgroups(t *testing.T) string {
	t.Helper()
	if cgroupErr != nil {
		t.Fatalf("cgroup v2 tree not prepared: %v", cgroupErr)
	}
	return cgroupRoot
}

// agentUser creates a real custom agent user with a fixed uid and removes
// it, with its home, when the test ends (B6e).
func agentUser(t *testing.T, name string, uid int) domain.Account {
	t.Helper()
	requireRoot(t)
	acc := domain.AccountFor(name, false).WithUID(uid)
	t.Cleanup(func() {
		_ = exec.Command("userdel", name).Run() // #nosec G204 -- fixed test user
		_ = os.RemoveAll(acc.Home)
	})
	if _, err := (&accounts.Accounts{}).Ensure(t.Context(), acc); err != nil {
		t.Fatalf("Ensure(%s) error = %v", name, err)
	}
	return acc
}

func passwdField(t *testing.T, file, name string, field int) string {
	t.Helper()
	f, err := os.Open(file) // #nosec G304 -- /etc/passwd or /etc/shadow
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		parts := strings.Split(sc.Text(), ":")
		if parts[0] == name && len(parts) > field {
			return parts[field]
		}
	}
	t.Fatalf("%s not in %s", name, file)
	return ""
}

func TestAccounts_CreatesRealUserWithPrivateHomeAndLocksIt(t *testing.T) {
	acc := agentUser(t, "aotest-mira", 200101)

	u, err := user.Lookup("aotest-mira")
	if err != nil || u.Uid != "200101" {
		t.Fatalf("user = %+v, %v; want uid 200101", u, err)
	}
	if shell := passwdField(t, "/etc/passwd", "aotest-mira", 6); shell != domain.NoLoginShell {
		t.Errorf("shell = %q, want %s", shell, domain.NoLoginShell)
	}
	info, err := os.Stat(acc.Home)
	if err != nil {
		t.Fatal(err)
	}
	st, _ := info.Sys().(*syscall.Stat_t)
	if info.Mode().Perm() != 0o700 || st.Uid != 200101 {
		t.Fatalf("home mode = %v owner = %d, want 0700 owned by 200101", info.Mode().Perm(), st.Uid)
	}
	if uid, err := (&accounts.Accounts{}).Ensure(t.Context(), acc); err != nil || uid != 200101 {
		t.Fatalf("second Ensure = %d, %v; want idempotent", uid, err)
	}
	if err := (&accounts.Accounts{}).Lock(t.Context(), acc); err != nil {
		t.Fatal(err)
	}
	if pw := passwdField(t, "/etc/shadow", "aotest-mira", 1); !strings.HasPrefix(pw, "!") {
		t.Errorf("shadow password = %q, want locked (!)", pw)
	}
	if _, err := os.Stat(acc.Home); err != nil {
		t.Errorf("home removed on lock: %v", err)
	}
}

// fifo is a named pipe any agent user may write to.
func fifo(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "ao")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
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
		b, _ := os.ReadFile(path) // #nosec G304 -- test FIFO
		got <- strings.TrimSpace(string(b))
	}()
	select {
	case s := <-got:
		return s
	case <-time.After(waitFor):
		if f, err := os.OpenFile(path, os.O_WRONLY, 0); err == nil { // #nosec G304 -- test FIFO
			_ = f.Close()
		}
		<-got
		t.Fatal("runner wrote nothing within 10s")
		return ""
	}
}

// run starts a shell runner for acc and stops it when the test ends.
func run(t *testing.T, acc domain.Account, script string, limits domain.Limits, root string) (*process.Launcher, string) {
	t.Helper()
	out := fifo(t)
	l := &process.Launcher{CgroupRoot: root}
	spec := domain.UnitSpec{Agent: acc.User, Account: acc, Limits: limits,
		Exec: domain.Exec{Path: "/bin/sh", Args: []string{"-c", script}, Env: []string{"OUT=" + out}}}
	if err := l.Install(t.Context(), spec); err != nil {
		t.Fatal(err)
	}
	if err := l.Start(t.Context(), acc.User); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Stop(context.Background(), acc.User) })
	return l, out
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
			n, _ := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
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

func TestProcessLauncher_RunsCustomAgentAsItsOwnUser(t *testing.T) {
	acc := agentUser(t, "aotest-nova", 200102)
	_, out := run(t, acc, `id -u > "$OUT"; exec sleep 600`, domain.Limits{}, "")
	if got := readFifo(t, out); got != "200102" {
		t.Fatalf("runner uid = %q, want 200102", got)
	}
}

// B6c: the memory limit is proven by a runner that tries to exceed it.
func TestCgroup_KillsRunnerThatExceedsItsMemoryLimit(t *testing.T) {
	root := requireCgroups(t)
	acc := agentUser(t, "aotest-vega", 200103)
	limits := domain.Limits{MemoryMaxBytes: 64 << 20}
	l, out := run(t, acc, `tail /dev/zero; echo "$?" > "$OUT"; exec sleep 600`, limits, root)

	status := readFifo(t, out)
	dir := filepath.Join(root, acc.User)
	kills := stat(t, dir, "memory.events", "oom_kill")
	t.Logf("proof memory: memory.max=%s memory.swap.max=%s; `tail /dev/zero` as uid 200103 -> exit=%s oom_kill=%d mode=%s",
		value(t, dir, "memory.max"), value(t, dir, "memory.swap.max"), status, kills, l.Mode(acc.User))
	if l.Mode(acc.User) != domain.LimitsHard || status != "137" || kills < 1 {
		t.Fatalf("mode=%s exit=%s oom_kill=%d; want hard, 137 (killed) and at least one OOM kill", l.Mode(acc.User), status, kills)
	}
}

// B6c: a fork bomb stops at pids.max.
func TestCgroup_StopsForkBombAtTaskLimit(t *testing.T) {
	root := requireCgroups(t)
	acc := agentUser(t, "aotest-orla", 200104)
	limits := domain.Limits{TasksMax: 20}
	script := `i=0; while [ "$i" -lt 100 ]; do sleep 60 & i=$((i+1)); done 2>/dev/null; echo forked > "$OUT"; exec sleep 600`
	l, out := run(t, acc, script, limits, root)

	readFifo(t, out)
	dir := filepath.Join(root, acc.User)
	current, _ := strconv.Atoi(value(t, dir, "pids.current"))
	refused := stat(t, dir, "pids.events", "max")
	t.Logf("proof pids: pids.max=%s; 100x `sleep 60 &` as uid 200104 -> pids.current=%d refused forks=%d mode=%s",
		value(t, dir, "pids.max"), current, refused, l.Mode(acc.User))
	if current > 20 || refused < 1 {
		t.Fatalf("pids.current=%d refused=%d; want at most 20 and refusals", current, refused)
	}
}

// B6c: a CPU spinner is held at its quota. Throttling needs wall time, so
// this test measures over a fixed window; it waits on nothing else.
func TestCgroup_HoldsCPUSpinnerAtQuota(t *testing.T) {
	root := requireCgroups(t)
	acc := agentUser(t, "aotest-pike", 200105)
	limits := domain.Limits{CPUQuotaPercent: 20}
	l, out := run(t, acc, `echo spinning > "$OUT"; while :; do :; done`, limits, root)

	readFifo(t, out)
	dir := filepath.Join(root, acc.User)
	before := stat(t, dir, "cpu.stat", "usage_usec")
	const window = 3 * time.Second
	// The measurement window: CPU use is a rate, so it needs wall time.
	// Nothing else is waited for.
	<-time.After(window)
	used := stat(t, dir, "cpu.stat", "usage_usec") - before
	throttled := stat(t, dir, "cpu.stat", "nr_throttled")
	share := float64(used) / float64(window.Microseconds())
	t.Logf("proof cpu: cpu.max=%q; spinner as uid 200105 used %d us of CPU in %v (%.0f%% of one CPU), nr_throttled=%d mode=%s",
		value(t, dir, "cpu.max"), used, window, share*100, throttled, l.Mode(acc.User))
	if share > 0.3 || throttled < 1 {
		t.Fatalf("CPU share %.2f, throttled %d; want at most 0.30 under a 20%% quota and throttling", share, throttled)
	}
}

func TestSocket_IsPrivateToTheAgentUser(t *testing.T) {
	agentUser(t, "aotest-lyra", 200106)
	dir, err := os.MkdirTemp("", "ao")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	srv := &socket.Server{Dir: dir}
	t.Cleanup(func() { _ = srv.Shutdown() })
	if err := srv.Listen(t.Context(), "aotest-lyra", 200106); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(srv.Path("aotest-lyra"))
	if err != nil {
		t.Fatal(err)
	}
	if st, _ := info.Sys().(*syscall.Stat_t); info.Mode().Perm() != 0o600 || st.Uid != 200106 {
		t.Fatalf("socket mode = %v owner = %d, want 0600 owned by 200106", info.Mode().Perm(), st.Uid)
	}
}
