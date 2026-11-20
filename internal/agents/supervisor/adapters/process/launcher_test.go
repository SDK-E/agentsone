// SPDX-License-Identifier: MIT

package process_test

import (
	"bytes"
	"context"
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
		serve()
	case "crash":
		_ = os.WriteFile(os.Getenv("HELPER_OUT"), []byte("started"), 0o600) // #nosec G703 -- FIFO path set by the test
		os.Exit(3)
	case "exit0":
		_ = os.WriteFile(os.Getenv("HELPER_OUT"), []byte("started"), 0o600) // #nosec G703 -- FIFO path set by the test
		os.Exit(0)
	case "crash-once":
		// The first run makes the HELPER_MARK directory (Mkdir fails when
		// it is there) and crashes; every later run serves, so a runner
		// started again stays up where a test sees it.
		if err := os.Mkdir(os.Getenv("HELPER_MARK"), 0o700); err != nil { // #nosec G703 -- marker path set by the test
			serve()
		}
		_ = os.WriteFile(os.Getenv("HELPER_OUT"), []byte("crashed"), 0o600) // #nosec G703 -- FIFO path set by the test
		os.Exit(3)
	}
}

// serve plays a runner that is up until SIGTERM.
func serve() {
	_ = os.WriteFile(os.Getenv("HELPER_OUT"), []byte(strings.Join(os.Environ(), "\n")), 0o600) // #nosec G703 -- FIFO path set by the test
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGTERM)
	<-ch
	os.Exit(0)
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
	for _, want := range []string{"AGENTSONE_RUNNER_SOCKET=/run/agentsone/runners/founder.sock", "AGENTSONE_AGENT=founder", "HOME=/root/agents/founder"} {
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

// exit is one runner exit the launcher reported.
type exit struct {
	name string
	err  error
}

// reporting is a launcher whose exit reports, and whose finished wait
// goroutines, the test can wait for.
func reporting() (l *process.Launcher, exits chan exit, done chan string) {
	exits, done = make(chan exit, 4), make(chan string, 4)
	l = &process.Launcher{Log: quiet, Exited: func(name string, err error) { exits <- exit{name, err} }}
	process.SetDoneHook(l, func(name string) { done <- name })
	return l, exits, done
}

func receive[T any](t *testing.T, ch <-chan T, what string) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(waitFor):
		t.Fatalf("no %s within 5s", what)
		var zero T
		return zero
	}
}

// crashOnce is a runner that crashes on its first run and serves on every
// later one, so a runner started again stays up where the test sees it.
func crashOnce(t *testing.T, out string) domain.UnitSpec {
	t.Helper()
	s := spec(t, "crash-once", out)
	s.Exec.Env = append(s.Exec.Env, "HELPER_MARK="+filepath.Join(t.TempDir(), "crashed"))
	return s
}

// The launcher has no restart policy (SDK-2082): a runner that fails is
// reported, and once the launcher is done with it nothing runs. A restart
// is the supervisor's call, so it passes the network wall check.
func TestLauncher_ReportsARunnerThatFailsAndNeverStartsItAgain(t *testing.T) {
	t.Parallel()
	out := fifo(t)
	l, exits, done := reporting()
	_ = l.Install(t.Context(), crashOnce(t, out))
	if err := l.Start(t.Context(), "founder"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Stop(context.Background(), "founder") })
	if got := started(t, out); got != "crashed" {
		t.Fatalf("first run wrote %q, want crashed", got)
	}

	e := receive(t, exits, "exit report")
	receive(t, done, "end of the runner's wait")

	if e.name != "founder" || e.err == nil || e.err.Error() != "exit status 3" {
		t.Fatalf("exit report = %s, %v; want founder, exit status 3", e.name, e.err)
	}
	if l.Running("founder") {
		t.Fatal("the launcher started the failed runner again by itself")
	}
}

func TestLauncher_ReportsACleanExitWithoutAnError(t *testing.T) {
	t.Parallel()
	out := fifo(t)
	l, exits, _ := reporting()
	_ = l.Install(t.Context(), spec(t, "exit0", out))
	if err := l.Start(t.Context(), "founder"); err != nil {
		t.Fatal(err)
	}
	started(t, out)

	if e := receive(t, exits, "exit report"); e.name != "founder" || e.err != nil {
		t.Fatalf("exit report = %s, %v; want founder, no error", e.name, e.err)
	}
}

func TestLauncher_DoesNotReportARunnerItStopped(t *testing.T) {
	t.Parallel()
	out := fifo(t)
	l, exits, done := reporting()
	_ = l.Install(t.Context(), spec(t, "serve", out))
	if err := l.Start(t.Context(), "founder"); err != nil {
		t.Fatal(err)
	}
	started(t, out)

	if err := l.Stop(t.Context(), "founder"); err != nil {
		t.Fatal(err)
	}
	receive(t, done, "end of the runner's wait")

	select {
	case e := <-exits:
		t.Fatalf("a stopped runner was reported as exited: %s, %v", e.name, e.err)
	default:
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

// Restart is Stop then Start. The hook holds the old runner's wait goroutine
// at the moment Stop returns, so the Start always comes before the rest of
// that goroutine: the launcher must already have let go of the old runner,
// or Start finds it, does nothing and no new runner starts.
func TestLauncher_RestartStartsANewRunnerWhileTheOldOneIsStillBeingReaped(t *testing.T) {
	t.Parallel()
	out := fifo(t)
	l := &process.Launcher{Log: quiet}
	first, release := make(chan struct{}, 1), make(chan struct{})
	first <- struct{}{}
	process.SetExitedHook(l, func(string) {
		select {
		case <-first: // hold only the first runner's goroutine
			<-release
		default:
		}
	})
	if err := l.Install(t.Context(), spec(t, "serve", out)); err != nil {
		t.Fatal(err)
	}
	if err := l.Start(t.Context(), "founder"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Stop(context.Background(), "founder") })
	t.Cleanup(func() { close(release) })
	started(t, out)
	if err := l.Restart(t.Context(), "founder"); err != nil {
		t.Fatal(err)
	}
	started(t, out) // the new runner
}

func TestLauncher_ReportsSoftLimitsWithoutCgroup(t *testing.T) {
	t.Parallel()
	out := fifo(t)
	s := spec(t, "serve", out)
	s.Account = domain.AccountFor("founder", false) // a custom agent; the test is not root, so no user switch
	s.Limits = domain.Limits{MemoryMaxBytes: 1 << 34, TasksMax: 4096}
	var log bytes.Buffer
	l := &process.Launcher{Log: slog.New(slog.NewTextHandler(&log, nil)), CgroupRoot: filepath.Join(t.TempDir(), "no-cgroup", "x")}
	_ = l.Install(t.Context(), s)
	if err := l.Start(t.Context(), "founder"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Stop(t.Context(), "founder") })
	env := started(t, out)
	if l.Mode("founder") != domain.LimitsSoft {
		t.Fatalf("mode = %s, want soft", l.Mode("founder"))
	}
	if want := "level=WARN msg=\"" + process.SoftLimitsLine + "\" agent=founder"; !strings.Contains(log.String(), want) {
		t.Errorf("log = %q, want %q", log.String(), want)
	}
	// A Go runner collects garbage before it reaches its memory limit.
	if want := "GOMEMLIMIT=12884901888"; !strings.Contains(env, want) {
		t.Errorf("runner environment %q lacks %q (3/4 of 16 GiB)", env, want)
	}
}

// A runner that sets its own GOMEMLIMIT keeps it.
func TestLauncher_KeepsTheRunnersOwnGoMemLimit(t *testing.T) {
	t.Parallel()
	out := fifo(t)
	s := spec(t, "serve", out)
	s.Account = domain.AccountFor("founder", false)
	s.Limits = domain.Limits{MemoryMaxBytes: 1 << 34}
	s.Exec.Env = append(s.Exec.Env, "GOMEMLIMIT=1GiB")
	l := &process.Launcher{Log: quiet}
	_ = l.Install(t.Context(), s)
	if err := l.Start(t.Context(), "founder"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Stop(t.Context(), "founder") })
	env := started(t, out)
	if n := strings.Count(env, "GOMEMLIMIT="); n != 1 || !strings.Contains(env, "GOMEMLIMIT=1GiB") {
		t.Errorf("runner environment %q, want only its own GOMEMLIMIT=1GiB", env)
	}
}

// A custom agent's runner that cannot start fails Start itself: on Linux
// the launch helper reports why before the runner would have run.
func TestLauncher_FailsStartOfCustomRunnerThatCannotRun(t *testing.T) {
	t.Parallel()
	// No limits: a custom account starts through the helper anyway. A
	// tasks limit would make the helper lower RLIMIT_NPROC for this test's
	// own user, which under `go test -race ./...` already runs more threads
	// than that, so the helper's runtime could not start a thread and died
	// before it reached the missing runner.
	s := spec(t, "serve", "/nonexistent")
	s.Account = domain.AccountFor("founder", false)
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
