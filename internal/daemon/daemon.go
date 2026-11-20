// SPDX-License-Identifier: MIT

// Package daemon wires the Agents One daemon: every module in start order,
// run through Provide, Migrate and Start, behind the API server. The
// command (cmd/agentsoned) and the in-process test harness both run this
// wiring, so a test that starts the daemon starts the one that ships.
package daemon

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/SDK-E/agentsone/internal/agents"
	"github.com/SDK-E/agentsone/internal/api"
	"github.com/SDK-E/agentsone/internal/audit"
	"github.com/SDK-E/agentsone/internal/auth"
	"github.com/SDK-E/agentsone/internal/bus"
	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/enginefetch"
	"github.com/SDK-E/agentsone/internal/events"
	"github.com/SDK-E/agentsone/internal/gatewaymint"
	"github.com/SDK-E/agentsone/internal/machine"
	"github.com/SDK-E/agentsone/internal/mcp"
	"github.com/SDK-E/agentsone/internal/memory"
	"github.com/SDK-E/agentsone/internal/plugins"
	"github.com/SDK-E/agentsone/internal/router"
	"github.com/SDK-E/agentsone/internal/sched"
)

// ShutdownGrace is how long the API listeners get to finish their requests
// once the daemon is asked to stop.
const ShutdownGrace = 10 * time.Second

// Option replaces one machine part of the wiring. The daemon runs with
// none. The in-process test harness (internal/daemontest) uses them so its
// tests touch no Linux users, packages or network of the host.
type Option func(*options)

type options struct {
	machine    func(core.Deps) core.Module
	supervisor agents.SupervisorOptions
	sched      []sched.Option
}

// WithMachine builds the machine module with newMachine instead of
// machine.New.
func WithMachine(newMachine func(core.Deps) core.Module) Option {
	return func(o *options) { o.machine = newMachine }
}

// WithSupervisor runs the agents' runner supervisor on so instead of the
// daemon's own parts.
func WithSupervisor(so agents.SupervisorOptions) Option {
	return func(o *options) { o.supervisor = so }
}

// WithSched passes opts to the sched module.
func WithSched(opts ...sched.Option) Option {
	return func(o *options) { o.sched = opts }
}

// Modules lists every daemon module in start order. Builders fill their own
// package; this list only changes through a contract change.
func Modules(deps core.Deps, opts ...Option) []core.Module {
	o := options{machine: func(d core.Deps) core.Module { return machine.New(d) }}
	for _, opt := range opts {
		opt(&o)
	}
	return []core.Module{
		audit.New(deps),
		gatewaymint.New(deps),
		agents.New(deps, agents.WithSupervisor(o.supervisor)),
		auth.New(deps),
		api.New(deps),
		bus.New(deps),
		events.New(deps),
		sched.New(deps, o.sched...),
		router.New(deps),
		memory.New(deps),
		plugins.New(deps),
		o.machine(deps),
		mcp.New(deps),
		enginefetch.New(deps),
	}
}

// Serve runs mods behind the API until ctx ends. Once every listener is
// open it logs "daemon ready" and calls ready, when not nil, with the
// listening addresses, socket first. The started modules stop in reverse
// order when Serve returns.
func Serve(ctx context.Context, deps core.Deps, mods []core.Module, ready func(addrs []net.Addr)) error {
	l := &lifecycle{mods: mods, log: deps.Log}
	defer l.stop(context.WithoutCancel(ctx))
	if err := l.start(ctx, deps.Ports); err != nil {
		return err
	}
	srv, err := api.NewServer(l.mods, deps)
	if err != nil {
		return fmt.Errorf("compose api: %w", err)
	}
	dispatchInProcess(l.mods, srv.Handler())
	running, err := srv.Listen()
	if err != nil {
		return err
	}
	errs := running.Serve()
	deps.Log.Info("daemon ready")
	if ready != nil {
		ready(running.Addrs())
	}
	select {
	case <-ctx.Done():
	case err := <-errs:
		if !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serve: %w", err)
		}
	}
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), ShutdownGrace)
	defer cancel()
	return running.Shutdown(shutdownCtx)
}

// lifecycle runs modules through Provide, Migrate and Start, and stops the
// started ones in reverse order.
type lifecycle struct {
	mods    []core.Module
	started []core.Module
	log     *slog.Logger
}

func (l *lifecycle) start(ctx context.Context, ports *core.Ports) error {
	for _, m := range l.mods {
		m.Provide(ports)
	}
	for _, m := range l.mods {
		if err := m.Migrate(ctx); err != nil {
			return fmt.Errorf("migrate %s: %w", m.Name(), err)
		}
	}
	for _, m := range l.mods {
		if err := m.Start(ctx); err != nil {
			return fmt.Errorf("start %s: %w", m.Name(), err)
		}
		l.started = append(l.started, m)
	}
	return nil
}

func (l *lifecycle) stop(ctx context.Context) {
	for i := len(l.started) - 1; i >= 0; i-- {
		m := l.started[i]
		if err := m.Stop(ctx); err != nil {
			l.log.Error("stop module", "module", m.Name(), "err", err)
		}
	}
	l.started = nil
}

// inProcess is a module that calls other modules' operations itself, such
// as the MCP server; it gets the composed handler, with no identity rules,
// and sets the caller on each request.
type inProcess interface{ SetHandler(http.Handler) }

func dispatchInProcess(mods []core.Module, h http.Handler) {
	for _, m := range mods {
		if d, ok := m.(inProcess); ok {
			d.SetHandler(h)
		}
	}
}
