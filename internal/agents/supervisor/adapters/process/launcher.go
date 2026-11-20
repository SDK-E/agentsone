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
// agents have no limits: they are trusted and could lift them anyway. The
// launcher never starts a runner by itself: a runner that exits without
// being stopped is reported to Exited, and the supervisor decides whether
// it starts again, through its network wall check.
package process

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/SDK-E/agentsone/internal/agents/supervisor/app"
	"github.com/SDK-E/agentsone/internal/agents/supervisor/domain"
	"github.com/SDK-E/agentsone/internal/core"
)

// HumansCgroup is the parent of one cgroup per human with a web terminal,
// next to AgentsCgroup.
const HumansCgroup = "humans"

// Defaults of the launcher.
const (
	DefaultStopTimeout = 10 * time.Second // TimeoutStopSec=10s
	DefaultNoFile      = 4096
)

// ErrNotInstalled is a start of an agent whose runner was never installed.
var ErrNotInstalled = errors.New("runner not installed")

// Launcher starts and stops runner processes and reports the ones that end
// by themselves. It has no restart policy.
type Launcher struct {
	// CgroupRoot is the cgroup v2 directory agent cgroups go under, for
	// example /sys/fs/cgroup/agentsone. Empty disables cgroups.
	CgroupRoot  string
	StopTimeout time.Duration
	Log         *slog.Logger
	// Exited, when set, hears every runner process that ended without
	// Stop: err says why (an exit status or a signal), nil for exit code 0.
	// It runs on the runner's wait goroutine once the launcher has let go
	// of the runner, so it may call Start. Set it before the first Start.
	Exited func(name string, err error)

	mu    sync.Mutex
	specs map[string]domain.UnitSpec
	procs map[string]*proc
	modes map[string]domain.LimitMode

	// exitedHook, when set by a test, runs in a runner's wait goroutine
	// right after Stop has been told that the runner exited. doneHook runs
	// as the last thing that goroutine does.
	exitedHook func(name string)
	doneHook   func(name string)
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

// AgentCell is where agent name's terminals run, so they count as the
// agent's: with a cgroup root, the runner's own cgroup <root>/<name>, made
// with the runner's limits when the runner is not running (hard). Without
// one, the same process limits the runner gets (soft). A root agent has no
// limits: its cell is its cgroup without limits, or nothing. An agent whose
// runner was never installed has no cell, and no terminal opens for it.
func (l *Launcher) AgentCell(_ context.Context, name string) (core.ProcessCell, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.init()
	spec, ok := l.specs[name]
	if !ok {
		return core.ProcessCell{}, fmt.Errorf("cell of %s: %w", name, ErrNotInstalled)
	}
	return cellFor(l.CgroupRoot, spec), nil
}

// HumanCell is where the human called name runs a web terminal: with a
// cgroup root, the cgroup <root>/../humans/<name>, made root-owned when it is
// missing. Without a cgroup root it is the zero cell: the terminal runs
// where the daemon runs, and the socket refuses it as any root process.
func (l *Launcher) HumanCell(_ context.Context, name string) (core.ProcessCell, error) {
	if l.CgroupRoot == "" {
		return core.ProcessCell{}, nil
	}
	if err := domain.ValidateName(name); err != nil {
		return core.ProcessCell{}, fmt.Errorf("cell of human %q: %w", name, err)
	}
	dir, err := humanCgroup(filepath.Join(filepath.Dir(l.CgroupRoot), HumansCgroup), name)
	if err != nil {
		return core.ProcessCell{}, fmt.Errorf("cell of human %s: %w", name, err)
	}
	return core.ProcessCell{Cgroup: dir}, nil
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
	return l.startLocked(name)
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

// wait reaps the runner and reports it when it ended by itself. It never
// starts a runner: a restart is the supervisor's call, made through Start.
func (l *Launcher) wait(name string, p *proc, sys *sysState) {
	if l.doneHook != nil {
		defer l.doneHook(name)
	}
	err := p.cmd.Wait()
	sys.cleanup()
	l.mu.Lock()
	if l.procs[name] == p {
		delete(l.procs, name)
	}
	byItself := !p.stopping
	l.mu.Unlock()
	// Tell Stop only once the runner is out of procs: Restart calls Start
	// right after Stop, and Start must not find the old runner there.
	close(p.exited)
	if l.exitedHook != nil {
		l.exitedHook(name)
	}
	if byItself && l.Exited != nil {
		l.Exited(name, err)
	}
}

// Stop ends the runner: SIGTERM to its process group, then SIGKILL after
// StopTimeout. Stopping a runner that is not running is not an error.
func (l *Launcher) Stop(ctx context.Context, name string) error {
	l.mu.Lock()
	l.init()
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
	return nil
}
