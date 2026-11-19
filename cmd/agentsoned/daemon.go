// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/SDK-E/agentsone/internal/api"
	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/store"
)

const shutdownGrace = 10 * time.Second

// openStores opens the four databases under the state folder.
func openStores(ctx context.Context, cfg core.Config) (core.Stores, func(), error) {
	if err := os.MkdirAll(cfg.StateDir, 0o750); err != nil {
		return core.Stores{}, nil, fmt.Errorf("create state folder: %w", err)
	}
	var opened []*store.DB
	closeAll := func() {
		for _, db := range opened {
			_ = db.Close() // closing at shutdown; nothing left to report to
		}
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
			closeAll()
			return core.Stores{}, nil, err
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

// listeners opens the Unix socket and the loopback TCP address.
func listeners(cfg core.Config) ([]net.Listener, error) {
	sock := cfg.SocketPath()
	if err := os.MkdirAll(filepath.Dir(sock), 0o750); err != nil {
		return nil, fmt.Errorf("create socket folder: %w", err)
	}
	if err := os.Remove(sock); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("remove stale socket: %w", err)
	}
	unix, err := net.Listen("unix", sock)
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", sock, err)
	}
	tcp, err := net.Listen("tcp", cfg.API.Addr)
	if err != nil {
		_ = unix.Close() // already failing; the listen error is the one to report
		return nil, fmt.Errorf("listen on %s: %w", cfg.API.Addr, err)
	}
	return []net.Listener{unix, tcp}, nil
}

// serve runs the daemon until ctx ends.
func serve(ctx context.Context, deps core.Deps) error {
	l := &lifecycle{mods: modules(deps), log: deps.Log}
	defer l.stop(context.WithoutCancel(ctx))
	if err := l.start(ctx, deps.Ports); err != nil {
		return err
	}
	handler, err := api.Compose(l.mods)
	if err != nil {
		return fmt.Errorf("compose api: %w", err)
	}
	lns, err := listeners(*deps.Config)
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	errs := make(chan error, len(lns))
	for _, ln := range lns {
		deps.Log.Info("listening", "addr", ln.Addr().String())
		go func() { errs <- srv.Serve(ln) }()
	}
	// The image boot test waits for this marker on the console.
	deps.Log.Info("AGENTSONE-READY")
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
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shut down: %w", err)
	}
	return nil
}
