// SPDX-License-Identifier: MIT

// Package process runs each agent's runner as a child process of the
// daemon (tini is PID 1 in the container; there is no systemd). A custom
// agent's runner runs as the agent's user. Its limits are enforced by a
// per-agent cgroup v2 at <CgroupRoot>/<agent> when the cgroup tree is
// writable (container option writable-cgroups=true). A custom agent's
// runner starts through the launch helper (see LaunchArgv0), which joins
// the cgroup, lowers rlimits and drops to the agent's user before it execs
// the runner, so the runner never runs unlimited or as root. Without a
// writable tree only
// process resource limits apply and the mode is reported as soft. Root
// agents have no limits: they are trusted and could lift them anyway. A
// runner that exits with a failure is restarted after RestartDelay.
package process

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/SDK-E/agentsone/internal/agents/supervisor/app"
	"github.com/SDK-E/agentsone/internal/agents/supervisor/domain"
)

// Defaults of the launcher.
const (
	DefaultRestartDelay = 2 * time.Second  // RestartSec=2s
	DefaultStopTimeout  = 10 * time.Second // TimeoutStopSec=10s
	DefaultNoFile       = 4096
)

// ErrNotInstalled is a start of an agent whose runner was never installed.
var ErrNotInstalled = errors.New("runner not installed")

// Launcher starts and supervises runner processes.
type Launcher struct {
	// CgroupRoot is the cgroup v2 directory agent cgroups go under, for
	// example /sys/fs/cgroup/agentsone. Empty disables cgroups.
	CgroupRoot   string
	RestartDelay time.Duration
	StopTimeout  time.Duration
	Log          *slog.Logger
	// After waits before a restart; nil means time.After. Tests inject it.
	After func(time.Duration) <-chan time.Time

	mu    sync.Mutex
	specs map[string]domain.UnitSpec
	procs map[string]*proc
	modes map[string]domain.LimitMode
	want  map[string]bool // Start was called and Stop was not since
}

var _ app.Launcher = (*Launcher)(nil)

type proc struct {
	cmd      *exec.Cmd
	stopping bool
	exited   chan struct{}
}

func (l *Launcher) init() {
	if l.specs == nil {
		l.specs, l.procs, l.modes = map[string]domain.UnitSpec{}, map[string]*proc{}, map[string]domain.LimitMode{}
		l.want = map[string]bool{}
	}
	if l.Log == nil {
		l.Log = slog.Default()
	}
}

// Install records how the agent's runner starts.
func (l *Launcher) Install(_ context.Context, spec domain.UnitSpec) error {
	if err := spec.Validate(); err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.init()
	l.specs[spec.Agent] = spec
	return nil
}

// Mode reports how agent name's limits hold: what the last start got, or
// before any start what it will get (root agents none, hard with a cgroup
// root, soft without).
func (l *Launcher) Mode(name string) domain.LimitMode {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.init()
	if m, ok := l.modes[name]; ok {
		return m
	}
	spec, ok := l.specs[name]
	switch {
	case !ok || spec.Account.IsRoot || spec.Limits == (domain.Limits{}):
		return domain.LimitsNone
	case l.CgroupRoot != "":
		return domain.LimitsHard
	default:
		return domain.LimitsSoft
	}
}

// Running reports whether agent name has a live runner process.
func (l *Launcher) Running(name string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.init()
	_, ok := l.procs[name]
	return ok
}

// Start starts the runner unless it is already running.
func (l *Launcher) Start(_ context.Context, name string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.init()
	if _, ok := l.procs[name]; ok {
		return nil
	}
	if err := l.startLocked(name); err != nil {
		return err
	}
	l.want[name] = true
	return nil
}

func (l *Launcher) startLocked(name string) error {
	spec, ok := l.specs[name]
	if !ok {
		return fmt.Errorf("start %s: %w", name, ErrNotInstalled)
	}
	cmd := exec.Command(spec.Exec.Path, spec.Exec.Args...) // #nosec G204 -- path and args come from the runner registry and passed Validate
	cmd.Env = environment(spec)
	cmd.Dir = spec.Exec.Dir
	if cmd.Dir == "" {
		cmd.Dir = spec.Account.Home
	}
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	sys, err := prepare(cmd, spec, l.CgroupRoot)
	if err != nil {
		return fmt.Errorf("start %s: %w", name, err)
	}
	if err := cmd.Start(); err != nil {
		sys.cleanup()
		return fmt.Errorf("start %s: %w", name, err)
	}
	mode, err := sys.afterStart()
	if err != nil {
		_ = cmd.Wait()
		sys.cleanup()
		return fmt.Errorf("start %s: %w", name, err)
	}
	if mode == domain.LimitsSoft {
		l.Log.Warn("agent limits are soft: no writable cgroup v2, CPU quota not enforced", "agent", name)
	}
	l.modes[name] = mode
	p := &proc{cmd: cmd, exited: make(chan struct{})}
	l.procs[name] = p
	go l.wait(name, p, sys)
	return nil
}

func environment(spec domain.UnitSpec) []string {
	env := []string{
		domain.SocketEnv + "=" + domain.SocketPath(spec.Agent),
		"AGENTSONE_AGENT=" + spec.Agent,
		"HOME=" + spec.Account.Home,
		"USER=" + spec.Account.User,
		"LOGNAME=" + spec.Account.User,
		"PATH=/usr/local/bin:/usr/bin:/bin",
	}
	for _, e := range spec.Exec.Env {
		if k, _, _ := strings.Cut(e, "="); k != domain.SocketEnv {
			env = append(env, e)
		}
	}
	return env
}

func (l *Launcher) wait(name string, p *proc, sys *sysState) {
	err := p.cmd.Wait()
	sys.cleanup()
	close(p.exited)
	l.mu.Lock()
	if l.procs[name] == p {
		delete(l.procs, name)
	}
	restart := !p.stopping && err != nil
	l.mu.Unlock()
	if !restart {
		return
	}
	l.Log.Warn("runner exited with a failure, restarting", "agent", name, "err", err)
	after := l.After
	if after == nil {
		after = time.After
	}
	delay := l.RestartDelay
	if delay == 0 {
		delay = DefaultRestartDelay
	}
	<-after(delay)
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, running := l.procs[name]; running {
		return
	}
	if !l.want[name] {
		return // stopped or removed while the restart waited
	}
	if _, installed := l.specs[name]; !installed {
		return
	}
	if err := l.startLocked(name); err != nil {
		l.Log.Error("runner restart failed", "agent", name, "err", err)
	}
}

// Stop ends the runner: SIGTERM to its process group, then SIGKILL after
// StopTimeout. Stopping a runner that is not running is not an error.
func (l *Launcher) Stop(ctx context.Context, name string) error {
	l.mu.Lock()
	l.init()
	l.want[name] = false
	p, ok := l.procs[name]
	if ok {
		p.stopping = true
	}
	l.mu.Unlock()
	if !ok {
		return nil
	}
	pgid := p.cmd.Process.Pid
	_ = syscall.Kill(-pgid, syscall.SIGTERM)
	timeout := l.StopTimeout
	if timeout == 0 {
		timeout = DefaultStopTimeout
	}
	t := time.NewTimer(timeout)
	defer t.Stop()
	select {
	case <-p.exited:
		return nil
	case <-t.C:
	case <-ctx.Done():
	}
	_ = syscall.Kill(-pgid, syscall.SIGKILL)
	<-p.exited
	return nil
}

// Restart stops and starts the runner.
func (l *Launcher) Restart(ctx context.Context, name string) error {
	if err := l.Stop(ctx, name); err != nil {
		return err
	}
	return l.Start(ctx, name)
}

// Remove forgets the runner. It must be stopped.
func (l *Launcher) Remove(_ context.Context, name string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.init()
	if _, ok := l.procs[name]; ok {
		return fmt.Errorf("remove %s: runner still running", name)
	}
	delete(l.specs, name)
	delete(l.modes, name)
	delete(l.want, name)
	return nil
}
