// SPDX-License-Identifier: MIT

// Package daemontest starts the whole daemon in process for integration
// tests: every real module, wired by internal/daemon exactly as the daemon
// that ships, on real SQLite stores in temporary folders, behind the real
// API on a Unix socket. Agents run the real echo runner, in process, over
// their real runner sockets and the runner protocol.
//
// Only the machine parts a test must not touch are replaced: the clock is
// fake, the machine module changes no packages or files of the host,
// custom agents get no Linux users, and the network wall holds. A test
// needs no root, no Docker and no network.
//
// See TESTING.md (B5) for when to use it instead of the coretest fakes.
package daemontest

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/agents"
	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/core/coretest"
	"github.com/SDK-E/agentsone/internal/daemon"
	"github.com/SDK-E/agentsone/internal/machine/machinetest"
	"github.com/SDK-E/agentsone/internal/sched"
	"github.com/SDK-E/agentsone/internal/store"
)

// Timeout bounds every wait of the harness. A wait is for a condition or a
// frame; the timeout only turns a hang into a failure. It is generous
// because CI runs every package at once under the race detector.
const Timeout = 30 * time.Second

// Daemon is a running daemon. Its fields are for the test to read and to
// move time; the daemon stops when the test ends.
type Daemon struct {
	// Clock is the daemon's clock. Only the test moves it.
	Clock *coretest.Clock
	// Config is the loaded config.toml.
	Config core.Config
	// Ports are the daemon's ports, filled by its real modules.
	Ports *core.Ports

	t       testing.TB
	ids     core.IDs
	runners *inProcess
	beat    chan time.Time
	logs    *logBuffer
	door    *webDoor
	tcp     string // the API's loopback address
	// stop stops the daemon and closes its stores, once.
	stop func()
	// setup is what Start's options set up. A restarted daemon keeps it, so
	// it reaches the same gateway and tool server forwarder.
	setup *startup
}

// webDoor stands in for the edge's web door, the only way the first human
// may be created (createFirstHuman is refused on the socket and on
// loopback). It is one more module that only takes the daemon's composed
// handler, the way the MCP module does, so the request still goes through
// every module's own rules.
type webDoor struct {
	core.Base
	mu sync.Mutex
	h  http.Handler
}

func (*webDoor) Name() string { return "daemontest-door" }

// SetHandler takes the composed handler from the daemon.
func (w *webDoor) SetHandler(h http.Handler) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.h = h
}

// serveAs runs req through the composed handler as caller.
func (w *webDoor) serveAs(req *http.Request, caller core.Caller) *httptest.ResponseRecorder {
	w.mu.Lock()
	h := w.h
	w.mu.Unlock()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req.WithContext(core.WithCaller(req.Context(), caller)))
	return rec
}

// Start runs the daemon until the test ends and returns once it is ready.
// The test fails when the daemon cannot start or does not stop cleanly.
// Options add machine parts, such as the model gateway (WithModelProvider).
func Start(t testing.TB, opts ...Option) *Daemon {
	t.Helper()
	cfg, cfgPath := loadConfig(t)
	clock := coretest.NewClock(coretest.Epoch)
	setup := &startup{t: t, cfg: &cfg, cfgPath: cfgPath, clock: clock}
	for _, o := range opts {
		o(setup)
	}
	return start(t, cfg, clock, coretest.NewIDs(), setup)
}

// Restart stops d, as a crash or a reboot would, and starts a new daemon on
// the same folders and stores, with the same clock and id source, so ids
// stay unique across the restart, and with the same machine parts from
// Start's options. Use the returned daemon from then on.
func (d *Daemon) Restart() *Daemon {
	d.t.Helper()
	d.stop()
	return start(d.t, d.Config, d.Clock, d.ids, d.setup)
}

// Stop stops d and closes its stores, for a test that changes the stores
// while no daemon runs (before Restart). Stopping twice is safe.
func (d *Daemon) Stop() { d.stop() }

func start(t testing.TB, cfg core.Config, clock *coretest.Clock, ids core.IDs, setup *startup) *Daemon {
	t.Helper()
	logs := &logBuffer{}
	d := &Daemon{
		Clock:   clock,
		Config:  cfg,
		Ports:   core.NewPorts(),
		t:       t,
		ids:     ids,
		runners: newInProcess(cfg.RuntimeDir),
		beat:    make(chan time.Time),
		logs:    logs,
		door:    &webDoor{},
		setup:   setup,
	}
	stores, closeStores := openStores(t, cfg)
	deps := core.Deps{
		Config: &d.Config,
		Log:    slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
		Clock:  d.Clock,
		IDs:    ids,
		Stores: stores,
		Ports:  d.Ports,
	}
	acc := &accounts{}
	mods := append(daemon.Modules(deps,
		daemon.WithMachine(func(deps core.Deps) core.Module { return machinetest.New(deps) }),
		daemon.WithSupervisor(agents.SupervisorOptions{
			Processes: d.runners, Accounts: acc, UserID: acc.userID, Wall: wallHolds{},
			Config: d.setup.supervisor,
		}),
		daemon.WithSched(sched.WithBeat(d.beat)),
	), d.door)

	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan []net.Addr, 1)
	done := make(chan error, 1)
	go func() { done <- daemon.Serve(ctx, deps, mods, func(addrs []net.Addr) { ready <- addrs }) }()
	select {
	case addrs := <-ready:
		for _, a := range addrs {
			if a.Network() == "tcp" {
				d.tcp = a.String()
			}
		}
	case err := <-done:
		cancel()
		d.runners.close()
		closeStores()
		t.Fatalf("the daemon stopped before it was ready: %v\n%s", err, logs)
	case <-time.After(Timeout):
		cancel()
		err := <-done
		d.runners.close()
		closeStores()
		t.Fatalf("the daemon was not ready within %s (%v)\n%s", Timeout, err, logs)
	}
	for _, ready := range d.setup.ready {
		ready(d)
	}
	d.stop = sync.OnceFunc(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("the daemon stopped with an error: %v", err)
		}
		d.runners.close()
		closeStores()
		if t.Failed() {
			t.Logf("daemon log:\n%s", logs)
		}
	})
	t.Cleanup(d.stop)
	return d
}

// FireSchedules moves the clock by d and makes the schedule clock look for
// due timers once. It returns when the check has started.
func (d *Daemon) FireSchedules(by time.Duration) {
	d.t.Helper()
	d.Clock.Advance(by)
	select {
	case d.beat <- d.Clock.Now():
	case <-time.After(Timeout):
		d.t.Fatalf("the schedule clock took no beat within %s", Timeout)
	}
}

// TCPAddr is the API's loopback address: where runners call the tool
// server and where the edge's web door forwards the interface.
func (d *Daemon) TCPAddr() string { return d.tcp }

// RunnerErrors are the in-process runners that stopped with an error.
func (d *Daemon) RunnerErrors() []error { return d.runners.runnerEnds() }

// loadConfig writes config.toml into temporary folders and loads it the
// way the daemon does. It returns the config and the file's path. The
// runtime folder holds the sockets, so it is short (TESTING.md B5c).
func loadConfig(t testing.TB) (core.Config, string) {
	t.Helper()
	dir := t.TempDir()
	run, err := os.MkdirTemp("", "ao")
	if err != nil {
		t.Fatalf("make the runtime folder: %v", err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(run); err != nil {
			t.Errorf("remove the runtime folder: %v", err)
		}
	})
	key := filepath.Join(dir, "gateway-key")
	if err := os.WriteFile(key, bytes.Repeat([]byte{0x42}, 32), 0o600); err != nil {
		t.Fatalf("write the gateway key: %v", err)
	}
	bundled := filepath.Join(dir, "bundled")
	if err := os.CopyFS(filepath.Join(bundled, "small-seed"), os.DirFS(filepath.Join(repoRoot(t), "plugins", "small-seed"))); err != nil {
		t.Fatalf("copy the small-seed plugin: %v", err)
	}
	toml := fmt.Sprintf(`state_dir = %q
runtime_dir = %q
lib_dir = %q
agents_home = %q

[api]
addr = "127.0.0.1:0"

[log]
level = "debug"

[gateway]
key_file = %q

[plugins]
bundled_dir = %q
`, filepath.Join(dir, "state"), run, filepath.Join(dir, "lib"), filepath.Join(dir, "agents"), key, bundled)
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(toml), 0o600); err != nil {
		t.Fatalf("write config.toml: %v", err)
	}
	cfg, err := core.LoadConfig(path)
	if err != nil {
		t.Fatalf("load config.toml: %v", err)
	}
	return cfg, path
}

// repoRoot is the repository's root folder, found from this file.
func repoRoot(t testing.TB) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot find the harness source folder")
	}
	return filepath.Join(filepath.Dir(file), "..", "..")
}

// openStores opens the daemon's stores and returns them with the function
// that closes them.
func openStores(t testing.TB, cfg core.Config) (core.Stores, func()) {
	t.Helper()
	if err := os.MkdirAll(cfg.StateDir, 0o750); err != nil {
		t.Fatalf("create the state folder: %v", err)
	}
	var closers []func()
	closeAll := func() {
		for _, c := range closers {
			c()
		}
	}
	open := func(name string) *store.DB {
		db, err := store.Open(context.Background(), cfg.DBPath(name))
		if err != nil {
			closeAll()
			t.Fatalf("open the %s store: %v", name, err)
		}
		closers = append(closers, func() {
			if err := db.Close(); err != nil {
				t.Errorf("close the %s store: %v", name, err)
			}
		})
		return db
	}
	stores := core.Stores{Core: open("core"), Bus: open("bus"), Memory: open("memory"), Audit: open("audit")}
	return stores, closeAll
}

// logBuffer keeps the daemon's log, shown only when a test fails.
type logBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (l *logBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *logBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}
