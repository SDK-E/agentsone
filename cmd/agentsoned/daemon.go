// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/SDK-E/agentsone/internal/api"
	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/store"
)

const shutdownGrace = 10 * time.Second

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

// serve runs the daemon until ctx ends.
func serve(ctx context.Context, deps core.Deps) error {
	return serveModules(ctx, deps, modules(deps))
}

// serveModules serves mods until ctx ends. Tests pass modules built on fakes.
func serveModules(ctx context.Context, deps core.Deps, mods []core.Module) error {
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
	sd := systemdFromEnv()
	if err := sd.notify("READY=1"); err != nil {
		deps.Log.Warn("systemd readiness", "err", err)
	}
	go sd.keepAlive(ctx, watchdogInterval(), func(err error) { deps.Log.Warn("systemd watchdog", "err", err) })
	select {
	case <-ctx.Done():
	case err := <-errs:
		if !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serve: %w", err)
		}
	}
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownGrace)
	defer cancel()
	return running.Shutdown(shutdownCtx)
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
