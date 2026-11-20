// SPDX-License-Identifier: MIT

package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/SDK-E/agentsone/internal/agents/app"
	"github.com/SDK-E/agentsone/internal/agents/domain"
	"github.com/SDK-E/agentsone/internal/agents/supervisor/adapters/accounts"
	"github.com/SDK-E/agentsone/internal/agents/supervisor/adapters/netwall"
	"github.com/SDK-E/agentsone/internal/agents/supervisor/adapters/process"
	"github.com/SDK-E/agentsone/internal/agents/supervisor/adapters/socket"
	"github.com/SDK-E/agentsone/internal/agents/supervisor/adapters/summaries"
	supapp "github.com/SDK-E/agentsone/internal/agents/supervisor/app"
	supdomain "github.com/SDK-E/agentsone/internal/agents/supervisor/domain"
	"github.com/SDK-E/agentsone/internal/core"
)

// defaultTick is how often the supervisor checks heartbeats and restarts
// failed runners whose wait is over; the first wait is 2 seconds, so the
// tick is shorter.
const defaultTick = time.Second

// SupervisorOptions are the machine parts the runner supervisor drives.
type SupervisorOptions struct {
	// Launcher runs runner processes. Nil means the daemon's own: child
	// processes (internal/agents/supervisor/adapters/process) behind one
	// Unix socket per agent under <RuntimeDir>/runners.
	Launcher supapp.Launcher
	// Accounts manages the custom agents' Linux users. Nil means the
	// system's (useradd and usermod).
	Accounts supapp.Accounts
	// Tick is how often heartbeats and due restarts are checked; zero
	// means 1 second.
	Tick time.Duration
	// Wall checks the machine's network wall at start and every 5 minutes.
	// Nil means the daemon's own: /proc (adapters/netwall).
	Wall supapp.NetworkWall
	// Processes runs the runners behind the daemon's own runner sockets,
	// when Launcher is nil. Nil means child processes
	// (adapters/process), which reports runners that end by themselves to
	// the supervisor. A test harness runs runners in process here and still
	// goes through the real sockets and protocol; it reports no exits.
	Processes supapp.Launcher
	// UserID finds the uid that owns a runner's socket, when Launcher is
	// nil. Nil means the system's user database.
	UserID func(user string) (int, error)
	// Config is the supervisor's settings, such as the addresses runners
	// get in their welcome. Nil means supapp.DefaultConfig().
	Config *supapp.Config
}

// WithSupervisor runs the society's agents through the runner supervisor
// (internal/agents/supervisor) on o.
func WithSupervisor(o SupervisorOptions) Option {
	return func(m *Module) { m.supOpts = &o }
}

// supervised is the society's Runtime over the supervisor. It exists
// before the supervisor does, since each needs the other.
type supervised struct{ sup *supapp.Supervisor }

var _ app.Runtime = (*supervised)(nil)

func (r *supervised) Provision(ctx context.Context, a core.Agent, l *domain.Limits) (int, error) {
	return r.sup.Provision(ctx, a, unitLimits(l))
}
func (r *supervised) Reconfigure(ctx context.Context, a core.Agent, l *domain.Limits) error {
	return r.sup.Reconfigure(ctx, a, unitLimits(l))
}
func (r *supervised) Retire(ctx context.Context, name string) error { return r.sup.Retire(ctx, name) }
func (r *supervised) Wake(ctx context.Context, name string, reason core.WakeReason, payload json.RawMessage) error {
	return r.sup.Wake(ctx, name, reason, payload)
}
func (r *supervised) Sleep(ctx context.Context, name string, compact bool) error {
	return r.sup.Sleep(ctx, name, compact)
}
func (r *supervised) Freeze(ctx context.Context, s core.FreezeScope) error {
	return r.sup.Freeze(ctx, s)
}
func (r *supervised) Release(ctx context.Context, s core.FreezeScope) error {
	return r.sup.Release(ctx, s)
}
func (r *supervised) Run(ctx context.Context, name string) error { return r.sup.Run(ctx, name) }

// unitLimits turns the society's limits into the runner unit's. Nil stays
// nil: the supervisor picks the defaults for custom agents.
func unitLimits(l *domain.Limits) *supdomain.Limits {
	if l == nil {
		return nil
	}
	return &supdomain.Limits{CPUQuotaPercent: l.CPUPercent, MemoryMaxBytes: int64(l.MemoryMB) << 20, TasksMax: l.Tasks}
}

// supervisorParts is what Start and Stop drive.
type supervisorParts struct {
	sup     *supapp.Supervisor
	sockets *socket.Server   // nil with an injected launcher
	wrapped *socket.Launcher // nil with an injected launcher
	tick    time.Duration
	stop    context.CancelFunc
	done    chan struct{}
}

// buildSupervisor makes the supervisor over the society. Called by New
// once the society exists.
func (m *Module) buildSupervisor(deps core.Deps, rt *supervised) {
	o := *m.supOpts
	parts := &supervisorParts{tick: o.Tick, stop: func() {}}
	if parts.tick <= 0 {
		parts.tick = defaultTick
	}
	launcher := o.Launcher
	var procs *process.Launcher
	if launcher == nil {
		dir := filepath.Join(core.RuntimeDir, "runners")
		if deps.Config != nil {
			dir = filepath.Join(deps.Config.RuntimeDir, "runners")
		}
		parts.sockets = &socket.Server{Dir: dir}
		var inner supapp.Launcher
		if o.Processes != nil {
			inner = o.Processes
		} else {
			procs = &process.Launcher{Log: m.log}
			inner = procs
		}
		parts.wrapped = &socket.Launcher{Inner: inner, Server: parts.sockets, UserID: o.UserID}
		launcher = parts.wrapped
	}
	cfg := supapp.DefaultConfig()
	if o.Config != nil {
		cfg = *o.Config
	}
	acc := o.Accounts
	if acc == nil {
		acc = &accounts.Accounts{}
	}
	wall := o.Wall
	if wall == nil {
		wall = netwall.System()
	}
	parts.sup = supapp.New(supapp.Deps{
		Clock: deps.Clock, IDs: deps.IDs, Ports: deps.Ports, Launcher: launcher, Accounts: acc,
		Identity: identity{s: m.society}, Output: output{s: m.society}, Summaries: summaries.New(m.db),
		Usage: usage{s: m.society}, Config: cfg, States: m.society,
		Wall: wall, Log: m.log,
	})
	if parts.sockets != nil {
		parts.sockets.Supervisor = parts.sup
	}
	if procs != nil {
		// The launcher only reports a runner that ended by itself; the
		// supervisor decides whether it starts again. The report comes on
		// the runner's own goroutine, after any call that started it.
		sup := parts.sup
		procs.Exited = func(name string, err error) { sup.RunnerExited(context.Background(), name, err) }
	}
	rt.sup = parts.sup
	m.parts = parts
}

// start checks the network wall before any agent can wake, binds the
// runner sockets to the module's life, and checks heartbeats (and the wall
// every 5 minutes) on a timer.
func (p *supervisorParts) start(ctx context.Context, log *slog.Logger) {
	p.sup.CheckWall(ctx)
	ctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	p.stop, p.done = cancel, make(chan struct{})
	if p.wrapped != nil {
		p.wrapped.Ctx = ctx
	}
	go func() {
		defer close(p.done)
		t := time.NewTicker(p.tick)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if err := p.sup.Tick(ctx); err != nil {
					log.Error("agents: runner supervisor tick", "err", err)
				}
			}
		}
	}()
}

// shutdown ends the timer and closes the runner sockets.
func (p *supervisorParts) shutdown() error {
	p.stop()
	if p.done != nil {
		<-p.done
	}
	if p.sockets == nil {
		return nil
	}
	if err := p.sockets.Shutdown(); err != nil {
		return fmt.Errorf("close runner sockets: %w", err)
	}
	return nil
}
