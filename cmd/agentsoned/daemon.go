// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"

	"github.com/SDK-E/agentsone/internal/agents"
	"github.com/SDK-E/agentsone/internal/agents/supervisor/adapters/process"
	supdomain "github.com/SDK-E/agentsone/internal/agents/supervisor/domain"
	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/daemon"
	"github.com/SDK-E/agentsone/internal/machine"
	machinedomain "github.com/SDK-E/agentsone/internal/machine/domain"
	"github.com/SDK-E/agentsone/internal/store"
)

// openStores opens the four databases under the state folder. The returned
// close function closes every opened database and reports each failure.
func openStores(ctx context.Context, cfg core.Config) (core.Stores, func() error, error) {
	if err := os.MkdirAll(cfg.StateDir, 0o750); err != nil {
		return core.Stores{}, nil, fmt.Errorf("create state folder: %w", err)
	}
	var opened []*store.DB
	closeAll := func() error {
		var errs []error
		for _, db := range opened {
			if err := db.Close(); err != nil {
				errs = append(errs, err)
			}
		}
		return errors.Join(errs...)
	}
	open := func(name string) (*store.DB, error) {
		db, err := store.Open(ctx, cfg.DBPath(name))
		if err != nil {
			return nil, fmt.Errorf("open %s database: %w", name, err)
		}
		opened = append(opened, db)
		return db, nil
	}
	var s core.Stores
	var err error
	for _, target := range []struct {
		name string
		db   **store.DB
	}{{"core", &s.Core}, {"bus", &s.Bus}, {"memory", &s.Memory}, {"audit", &s.Audit}} {
		if *target.db, err = open(target.name); err != nil {
			return core.Stores{}, nil, errors.Join(err, closeAll())
		}
	}
	return s, closeAll, nil
}

// serve runs the daemon until ctx ends, with limits as the boot probe
// decided them.
func serve(ctx context.Context, deps core.Deps, limits process.Boot) error {
	return serveModules(ctx, deps, modules(deps, limits))
}

// modules are the daemon's modules, as internal/daemon wires them. Custom
// agents' cgroups go under limits.Root, the root the boot probe prepared
// (empty means soft limits), and the machine module reports how the limits
// hold in its metrics.
func modules(deps core.Deps, limits process.Boot) []core.Module {
	held := agentLimits(limits)
	return daemon.Modules(deps,
		daemon.WithSupervisor(agents.SupervisorOptions{CgroupRoot: limits.Root}),
		daemon.WithMachine(func(d core.Deps) core.Module { return machine.New(d, machine.WithAgentLimits(held)) }),
	)
}

// agentLimits is how the machine module reports limits: hard, or soft with
// plain words on why and how to make them hard.
func agentLimits(b process.Boot) machinedomain.AgentLimits {
	if b.Mode == supdomain.LimitsHard {
		return machinedomain.HardLimits()
	}
	return machinedomain.SoftLimits(b.Reason.Advice())
}

// serveModules serves mods until ctx ends, and tells systemd once the
// daemon is ready. Tests pass modules built on fakes.
func serveModules(ctx context.Context, deps core.Deps, mods []core.Module) error {
	return daemon.Serve(ctx, deps, mods, func([]net.Addr) { notifyReady(ctx, deps.Log) })
}

// notifyReady tells systemd the daemon is ready and pings its watchdog
// until ctx ends.
func notifyReady(ctx context.Context, log *slog.Logger) {
	sd := systemdFromEnv()
	if err := sd.notify("READY=1"); err != nil {
		log.Warn("systemd readiness", "err", err)
	}
	go sd.keepAlive(ctx, watchdogInterval(), func(err error) { log.Warn("systemd watchdog", "err", err) })
}
