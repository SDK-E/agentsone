// SPDX-License-Identifier: MIT

package daemontest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"sync"

	supapp "github.com/SDK-E/agentsone/internal/agents/supervisor/app"
	"github.com/SDK-E/agentsone/internal/agents/supervisor/domain"
	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/runners/claude"
	"github.com/SDK-E/agentsone/runners/echo"
	"github.com/SDK-E/agentsone/runners/openai"
	"github.com/SDK-E/agentsone/runners/sdk"
)

// runnerSocketEnv names the variable the runner adapter puts the agent's
// socket path in (internal/plugins/domain.RunnerSocketEnv, sdk.EnvSocket).
const runnerSocketEnv = sdk.EnvSocket

// inProcess runs each agent's runner as a goroutine instead of a child
// process. The runner dials the agent's own socket, which the daemon's
// socket server opened, so the supervisor, the socket server, the runner
// protocol and the runner are all the real ones. The runner is picked by the
// file name of the command the runner adapter gave: echo runs runners/echo.
type inProcess struct {
	// runtimeDir is the harness's runtime folder, which holds the daemon's
	// runner sockets.
	runtimeDir string

	mu      sync.Mutex
	specs   map[string]domain.UnitSpec
	running map[string]*runnerRun
	ends    []error
	wg      sync.WaitGroup
}

type runnerRun struct {
	cancel context.CancelFunc
	done   chan struct{}
}

var _ supapp.Launcher = (*inProcess)(nil)

func newInProcess(runtimeDir string) *inProcess {
	return &inProcess{runtimeDir: runtimeDir, specs: map[string]domain.UnitSpec{}, running: map[string]*runnerRun{}}
}

// Install keeps how the agent's runner starts.
func (l *inProcess) Install(_ context.Context, spec domain.UnitSpec) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.specs[spec.Agent] = spec
	return nil
}

// Start runs the agent's runner unless it already runs.
func (l *inProcess) Start(_ context.Context, agent string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	spec, ok := l.specs[agent]
	if !ok {
		return fmt.Errorf("start %s: no runner installed", agent)
	}
	if _, ok := l.running[agent]; ok {
		return nil
	}
	opts, h, err := runnerFor(spec, l.runtimeDir)
	if err != nil {
		return fmt.Errorf("start %s: %w", agent, err)
	}
	socket, err := socketOf(spec)
	if err != nil {
		return fmt.Errorf("start %s: %w", agent, err)
	}
	// The runner is told the path in the machine's runtime folder; the
	// harness's runtime folder stands in for it. A path that drifts from
	// where the socket server listens fails here as it would on a machine.
	rel, err := filepath.Rel(core.RuntimeDir, socket)
	if err != nil || strings.HasPrefix(rel, "..") {
		return fmt.Errorf("start %s: runner socket %s is outside %s", agent, socket, core.RuntimeDir)
	}
	opts.Socket = filepath.Join(l.runtimeDir, rel)
	ctx, cancel := context.WithCancel(context.Background())
	r := &runnerRun{cancel: cancel, done: make(chan struct{})}
	l.running[agent] = r
	l.wg.Add(1)
	go func() {
		defer l.wg.Done()
		defer close(r.done)
		err := sdk.Run(ctx, opts, h)
		// A runner process ends with its handler, as the openai runner's
		// main closes its tool server session.
		if c, ok := h.(io.Closer); ok {
			err = errors.Join(err, c.Close())
		}
		l.mu.Lock()
		defer l.mu.Unlock()
		if l.running[agent] == r {
			delete(l.running, agent)
		}
		if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, sdk.ErrDaemonClosed) {
			l.ends = append(l.ends, fmt.Errorf("runner of %s: %w", agent, err))
		}
	}()
	return nil
}

// Stop ends the agent's runner and waits until it has ended.
func (l *inProcess) Stop(_ context.Context, agent string) error {
	l.mu.Lock()
	r, ok := l.running[agent]
	l.mu.Unlock()
	if !ok {
		return nil
	}
	r.cancel()
	<-r.done
	return nil
}

// Restart stops the runner and starts it again.
func (l *inProcess) Restart(ctx context.Context, agent string) error {
	if err := l.Stop(ctx, agent); err != nil {
		return err
	}
	return l.Start(ctx, agent)
}

// Remove forgets the runner. It must be stopped.
func (l *inProcess) Remove(_ context.Context, agent string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.specs, agent)
	return nil
}

// Mode is "none" for root agents and "soft" for custom agents: in-process
// runners have no cgroup, like a machine without writable cgroups.
func (l *inProcess) Mode(agent string) domain.LimitMode {
	l.mu.Lock()
	defer l.mu.Unlock()
	if spec, ok := l.specs[agent]; ok && spec.Account.IsRoot {
		return domain.LimitsNone
	}
	return domain.LimitsSoft
}

// close stops every runner and waits for them all.
func (l *inProcess) close() {
	l.mu.Lock()
	for _, r := range l.running {
		r.cancel()
	}
	l.mu.Unlock()
	l.wg.Wait()
}

// runnerEnds are the runners that stopped with an error, for failure
// messages.
func (l *inProcess) runnerEnds() []error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]error(nil), l.ends...)
}

// runnerFor picks the runner of spec. runtimeDir holds the claude runner's
// state; its engine is never installed in the harness, as on a machine
// where no human turned the Claude engine on.
func runnerFor(spec domain.UnitSpec, runtimeDir string) (sdk.Options, sdk.Handler, error) {
	switch name := filepath.Base(spec.Exec.Path); name {
	case "echo":
		return echo.Options(), echo.New(), nil
	case "openai":
		// It reaches the model gateway and the tool server at the addresses
		// of its welcome; WithModelProvider makes them the harness's.
		return openai.Options(), openai.New(openai.Config{}), nil
	case "claude":
		state := filepath.Join(runtimeDir, "claude-"+spec.Agent)
		return claude.Options(), claude.New(claude.Config{
			EnginePath: filepath.Join(state, NoEngine),
			StateDir:   state,
			Log:        func(string) {},
		}), nil
	default:
		return sdk.Options{}, nil, fmt.Errorf("runner %q does not run in process; the harness runs echo, openai and claude", name)
	}
}

// NoEngine is the file name the harness gives the claude runner as its
// engine. The file never exists, so every turn of a claude agent meets a
// machine whose Claude engine is not installed.
const NoEngine = "claude-engine-not-installed"

// socketOf is the socket path the runner adapter told the runner to dial.
func socketOf(spec domain.UnitSpec) (string, error) {
	for _, kv := range spec.Exec.Env {
		if v, ok := strings.CutPrefix(kv, runnerSocketEnv+"="); ok {
			return v, nil
		}
	}
	return "", fmt.Errorf("the runner's environment has no %s", runnerSocketEnv)
}

// accounts is the supervisor's Accounts without Linux users: it keeps the
// uid the society gave each custom agent, and answers the socket server's
// uid lookups from it.
type accounts struct {
	mu   sync.Mutex
	uids map[string]int
}

var _ supapp.Accounts = (*accounts)(nil)

func (a *accounts) Ensure(_ context.Context, acc domain.Account) (int, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.uids == nil {
		a.uids = map[string]int{}
	}
	a.uids[acc.User] = acc.UID
	return acc.UID, nil
}

func (a *accounts) EnsureRootHome(context.Context, domain.Account) error { return nil }

func (a *accounts) Lock(context.Context, domain.Account) error { return nil }

// userID is the uid of user, as Ensure made it.
func (a *accounts) userID(user string) (int, error) {
	if user == "root" {
		return 0, nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	uid, ok := a.uids[user]
	if !ok {
		return 0, fmt.Errorf("look up user %s: no such user", user)
	}
	return uid, nil
}

// wallHolds is a network wall that always holds, as on a machine whose only
// way out is the edge.
type wallHolds struct{}

func (wallHolds) Check(context.Context) domain.NetWall {
	return domain.NetWall{Holds: true, Interfaces: []string{"eth0"}}
}
