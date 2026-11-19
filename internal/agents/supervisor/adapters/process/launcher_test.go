// SPDX-License-Identifier: MIT

package process_test

import (
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/agents/supervisor/adapters/process"
	"github.com/SDK-E/agentsone/internal/agents/supervisor/domain"
)

// TestMain doubles as the fake runner: with HELPER_MODE set, the test
// binary plays a runner process instead of running tests. It reports to
// the test through the FIFO named by HELPER_OUT, so the test waits on the
// FIFO instead of polling.
func TestMain(m *testing.M) {
	switch os.Getenv("HELPER_MODE") {
	case "":
		os.Exit(m.Run())
	case "serve":
		_ = os.WriteFile(os.Getenv("HELPER_OUT"), []byte(strings.Join(os.Environ(), "\n")), 0o600) // #nosec G703 -- FIFO path set by the test
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, syscall.SIGTERM)
		<-ch
		os.Exit(0)
	case "crash":
		_ = os.WriteFile(os.Getenv("HELPER_OUT"), []byte("started"), 0o600) // #nosec G703 -- FIFO path set by the test
		os.Exit(3)
	}
}

const waitFor = 5 * time.Second

var quiet = slog.New(slog.DiscardHandler)

// fifo makes a named pipe the helper writes to when it starts.
func fifo(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "ao")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "out")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// started waits until a runner process wrote to the FIFO and returns what it wrote.
func started(t *testing.T, path string) string {
	t.Helper()
	got := make(chan []byte, 1)
	go func() {
		b, _ := os.ReadFile(path) // #nosec G304 -- test FIFO; blocks until a runner opens it and closes it
		got <- b
	}()
	select {
	case b := <-got:
		return string(b)
	case <-time.After(waitFor):
		// Unblock the reader so its goroutine ends with the test.
		if f, err := os.OpenFile(path, os.O_WRONLY, 0); err == nil { // #nosec G304 -- test FIFO
			_ = f.Close()
		}
		<-got
		t.Fatal("no runner started within 5s")
		return ""
	}
}

func spec(t *testing.T, mode, out string) domain.UnitSpec {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return domain.UnitSpec{
		Agent: "founder", Account: domain.AccountFor("founder", true),
		// GOCOVERDIR keeps a coverage-built helper from warning on stderr.
		Exec: domain.Exec{Path: exe, Env: []string{"HELPER_MODE=" + mode, "HELPER_OUT=" + out, "GOCOVERDIR=" + t.TempDir()}, Dir: t.TempDir()},
	}
}

func TestLauncher_StartsRunnerWithSocketEnvironmentAndStopsIt(t *testing.T) {
	t.Parallel()
	out := fifo(t)
	l := &process.Launcher{Log: quiet}
	if err := l.Install(t.Context(), spec(t, "serve", out)); err != nil {
		t.Fatal(err)
	}
	if err := l.Start(t.Context(), "founder"); err != nil {
		t.Fatal(err)
	}
	if err := l.Start(t.Context(), "founder"); err != nil {
		t.Fatalf("second Start error = %v, want no-op", err)
	}
	env := started(t, out)
	for _, want := range []string{"AGENTSONE_RUNNER_SOCKET=/run/agentsone/runners/founder.sock", "AGENTSONE_AGENT=founder", "HOME=/root"} {
		if !strings.Contains(env, want) {
			t.Errorf("runner environment lacks %q", want)
		}
	}
	if l.Mode("founder") != domain.LimitsNone {
		t.Errorf("root agent limit mode = %s, want none", l.Mode("founder"))
	}

	if err := l.Stop(t.Context(), "founder"); err != nil {
		t.Fatal(err)
	}
	if l.Running("founder") {
		t.Fatal("runner still running after Stop")
	}
	if err := l.Stop(t.Context(), "founder"); err != nil {
		t.Fatalf("Stop of a stopped runner error = %v", err)
	}
	if err := l.Remove(t.Context(), "founder"); err != nil {
		t.Fatal(err)
	}
	if err := l.Start(t.Context(), "founder"); !errors.Is(err, process.ErrNotInstalled) {
		t.Fatalf("Start after Remove error = %v, want ErrNotInstalled", err)
	}
}

// Restart=on-failure: a runner that exits with a failure is started again after the delay.
func TestLauncher_RestartsRunnerThatFails(t *testing.T) {
	t.Parallel()
	out := fifo(t)
	delays := make(chan chan time.Time, 4)
	l := &process.Launcher{Log: quiet, After: func(time.Duration) <-chan time.Time {
		ch := make(chan time.Time, 1)
		delays <- ch
		return ch
	}}
	_ = l.Install(t.Context(), spec(t, "crash", out))
	if err := l.Start(t.Context(), "founder"); err != nil {
		t.Fatal(err)
	}
	restartScheduled := func() chan time.Time {
		select {
		case ch := <-delays:
			return ch
		case <-time.After(waitFor):
			t.Fatal("no restart scheduled within 5s")
			return nil
		}
	}
	started(t, out)
	restartScheduled() <- time.Time{}
	started(t, out) // the restart ran
	// The second crash schedules another restart; the runner is gone by then.
	pending := restartScheduled()
	if l.Running("founder") {
		t.Fatal("crashed runner still reported running")
	}
	if err := l.Remove(t.Context(), "founder"); err != nil {
		t.Fatal(err)
	}
	pending <- time.Time{} // after Remove the pending restart starts nothing
	if err := l.Start(t.Context(), "founder"); !errors.Is(err, process.ErrNotInstalled) {
		t.Fatalf("Start after Remove error = %v", err)
	}
}

func TestLauncher_RefusesUnsafeSpecAndRemovingRunningRunner(t *testing.T) {
	t.Parallel()
	l := &process.Launcher{Log: quiet}
	bad := domain.UnitSpec{Agent: "founder", Account: domain.AccountFor("founder", true), Exec: domain.Exec{Path: "relative"}}
	if err := l.Install(t.Context(), bad); !errors.Is(err, domain.ErrUnsafeValue) {
		t.Fatalf("Install error = %v, want ErrUnsafeValue", err)
	}
	out := fifo(t)
	_ = l.Install(t.Context(), spec(t, "serve", out))
	_ = l.Start(t.Context(), "founder")
	t.Cleanup(func() { _ = l.Stop(t.Context(), "founder") })
	started(t, out)
	if err := l.Remove(t.Context(), "founder"); err == nil {
		t.Fatal("Remove of a running runner succeeded")
	}
	if err := l.Restart(t.Context(), "founder"); err != nil || !l.Running("founder") {
		t.Fatalf("Restart error = %v running = %v", err, l.Running("founder"))
	}
	started(t, out)
}

func TestLauncher_ReportsSoftLimitsWithoutCgroup(t *testing.T) {
	t.Parallel()
	out := fifo(t)
	s := spec(t, "serve", out)
	s.Account = domain.AccountFor("founder", false) // a custom agent; the test is not root, so no user switch
	s.Limits = domain.Limits{MemoryMaxBytes: 1 << 34, TasksMax: 4096}
	l := &process.Launcher{Log: quiet, CgroupRoot: filepath.Join(t.TempDir(), "no-cgroup", "x")}
	_ = l.Install(t.Context(), s)
	if err := l.Start(t.Context(), "founder"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Stop(t.Context(), "founder") })
	started(t, out)
	if l.Mode("founder") != domain.LimitsSoft {
		t.Fatalf("mode = %s, want soft", l.Mode("founder"))
	}
}

// A custom agent's runner that cannot start fails Start itself: on Linux
// the launch helper reports why before the runner would have run.
func TestLauncher_FailsStartOfCustomRunnerThatCannotRun(t *testing.T) {
	t.Parallel()
	s := spec(t, "serve", "/nonexistent")
	s.Account = domain.AccountFor("founder", false)
	s.Limits = domain.Limits{TasksMax: 64}
	s.Exec.Path = filepath.Join(t.TempDir(), "missing-runner")
	l := &process.Launcher{Log: quiet}
	_ = l.Install(t.Context(), s)
	if err := l.Start(t.Context(), "founder"); err == nil || !strings.Contains(err.Error(), "missing-runner") {
		t.Fatalf("Start error = %v, want one naming the missing runner", err)
	}
	if l.Running("founder") {
		t.Fatal("a runner that never started is reported running")
	}
}

func TestLauncher_PredictsModeBeforeStart(t *testing.T) {
	t.Parallel()
	custom := spec(t, "serve", "/nonexistent")
	custom.Account = domain.AccountFor("founder", false)
	custom.Limits = domain.DefaultCustomLimits
	tests := []struct {
		name   string
		spec   *domain.UnitSpec
		cgroup string
		want   domain.LimitMode
	}{
		{"not installed", nil, "/sys/fs/cgroup/agents", domain.LimitsNone},
		{"custom with a cgroup root", &custom, "/sys/fs/cgroup/agents", domain.LimitsHard},
		{"custom without a cgroup root", &custom, "", domain.LimitsSoft},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			l := &process.Launcher{Log: quiet, CgroupRoot: tt.cgroup}
			if tt.spec != nil {
				_ = l.Install(t.Context(), *tt.spec)
			}
			if got := l.Mode("founder"); got != tt.want {
				t.Fatalf("Mode() = %s, want %s", got, tt.want)
			}
		})
	}
}
