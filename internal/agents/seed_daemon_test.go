// SPDX-License-Identifier: MIT

package agents_test

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/SDK-E/agentsone/internal/agents"
	"github.com/SDK-E/agentsone/internal/agents/agentstest"
	"github.com/SDK-E/agentsone/internal/agents/app"
	authsqlite "github.com/SDK-E/agentsone/internal/auth/adapters/sqlite"
	authdomain "github.com/SDK-E/agentsone/internal/auth/domain"
	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/core/coretest"
	"github.com/SDK-E/agentsone/internal/daemon"
	"github.com/SDK-E/agentsone/internal/machine/machinetest"
	"github.com/SDK-E/agentsone/internal/store"
)

// seedMachine is one machine's folders and config, run as a real daemon
// several times in a row, as restarts do.
type seedMachine struct {
	configPath string
	logs       *seedLog
}

// newSeedMachine writes a config whose bundled plugins are the repository's
// small-seed plugin, in folders of the test's own.
func newSeedMachine(t *testing.T) *seedMachine {
	t.Helper()
	dir := t.TempDir()
	// The API socket lives in the runtime folder; macOS keeps socket paths
	// under 104 bytes, which t.TempDir names can pass.
	run, err := os.MkdirTemp("", "ao")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { coretest.Check(t, func() error { return os.RemoveAll(run) }) })
	bundled := filepath.Join(dir, "lib", "plugins")
	if err := os.CopyFS(filepath.Join(bundled, "small-seed"), os.DirFS("../../plugins/small-seed")); err != nil {
		t.Fatal(err)
	}
	config := fmt.Sprintf(`state_dir = %q
runtime_dir = %q
lib_dir = %q
agents_home = %q

[api]
addr = "127.0.0.1:0"

[plugins]
bundled_dir = %q
`, filepath.Join(dir, "state"), run, filepath.Join(dir, "lib"), filepath.Join(dir, "agents"), bundled)
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	return &seedMachine{configPath: path, logs: &seedLog{}}
}

func (sm *seedMachine) config(t *testing.T) core.Config {
	t.Helper()
	cfg, err := core.LoadConfig(sm.configPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cfg.StateDir, 0o750); err != nil {
		t.Fatal(err)
	}
	return cfg
}

// openDB opens one of the machine's databases until the test ends.
func openDB(t *testing.T, cfg core.Config, name string) *store.DB {
	t.Helper()
	db, err := store.Open(context.Background(), cfg.DBPath(name))
	if err != nil {
		t.Fatalf("open %s database: %v", name, err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close %s database: %v", name, err)
		}
	})
	return db
}

// addHumanAsAnOlderBuild leaves the machine as an older build did: a human
// in auth's table, and neither a first-human event nor a society.
func (sm *seedMachine) addHumanAsAnOlderBuild(t *testing.T) {
	t.Helper()
	db := openDB(t, sm.config(t), "core")
	ctx := context.Background()
	if err := authsqlite.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	ada := authdomain.Human{Name: "ada", DisplayName: "Ada", UID: 1000, CreatedAt: time.Now().UTC()}
	if err := authsqlite.New(db).AddHuman(ctx, ada); err != nil {
		t.Fatal(err)
	}
}

// start runs the daemon's real modules on the machine's databases until it
// is ready and the seed has ended, calls during with the society, and stops
// the daemon. Only the runner runtime and the machine's system are test
// doubles: a test never creates Linux users, runs runners or changes the
// host's packages.
func (sm *seedMachine) start(t *testing.T, during func(*app.Society)) {
	t.Helper()
	cfg := sm.config(t)
	deps := core.Deps{
		Config: &cfg, Log: slog.New(sm.logs), Clock: core.SystemClock{}, IDs: core.SystemIDs(),
		Stores: core.Stores{Core: openDB(t, cfg, "core"), Bus: openDB(t, cfg, "bus"), Memory: openDB(t, cfg, "memory"), Audit: openDB(t, cfg, "audit")},
		Ports:  core.NewPorts(),
	}
	society := agents.New(deps, agents.WithRuntime(agentstest.NewRuntime()))
	mods := daemon.Modules(deps)
	for i, m := range mods {
		switch m.Name() {
		case "agents":
			mods[i] = society
		case "machine":
			mods[i] = machinetest.New(deps)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := make(chan struct{})
	done := make(chan error, 1)
	go func() { done <- daemon.Serve(ctx, deps, mods, func([]net.Addr) { close(ready) }) }()
	select {
	case <-ready:
	case err := <-done:
		t.Fatalf("daemon stopped before it was ready: %v", err)
	case <-time.After(30 * time.Second):
		t.Fatal("daemon not ready after 30s")
	}
	waitSeed(t, society)
	during(society.Society())
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("daemon: %v", err)
	}
}

func activeAgents(t *testing.T, s *app.Society) []string {
	t.Helper()
	list, err := s.List(context.Background(), app.ListFilter{})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, a := range list {
		if a.Active() {
			names = append(names, a.Name)
		}
	}
	return names
}

// The demo machine's bug: its first human came from an older build, so the
// seed never ran and the society stayed empty. Now the daemon seeds it at
// the next start, once: a restart seeds nothing more, and a society the
// founder emptied stays empty.
func TestDaemon_SeedsTheSocietyAtStartWhenAHumanExists(t *testing.T) {
	sm := newSeedMachine(t)
	sm.addHumanAsAnOlderBuild(t)
	const seeded = "INFO agents: seeded the society"

	sm.start(t, func(s *app.Society) {
		if diff := cmp.Diff([]string{"founder", "router", "waker"}, activeAgents(t, s)); diff != "" {
			t.Errorf("first start agents (-want +got):\n%s", diff)
		}
	})
	if got := sm.logs.count(seeded); got != 1 {
		t.Fatalf("first start logged the seed %d times, want once", got)
	}

	sm.start(t, func(s *app.Society) {
		if diff := cmp.Diff([]string{"founder", "router", "waker"}, activeAgents(t, s)); diff != "" {
			t.Errorf("restart agents (-want +got):\n%s", diff)
		}
		founder := core.WithCaller(context.Background(), core.Caller{Kind: core.CallerAgent, Name: "founder", UID: -1, IsRoot: true})
		for _, name := range []string{"router", "waker"} {
			if err := s.Retire(founder, name); err != nil {
				t.Fatalf("retire %s: %v", name, err)
			}
		}
	})

	sm.start(t, func(s *app.Society) {
		if diff := cmp.Diff([]string{"founder"}, activeAgents(t, s)); diff != "" {
			t.Errorf("after the founder emptied its team, agents (-want +got):\n%s", diff)
		}
	})
	if got := sm.logs.count(seeded); got != 1 {
		t.Errorf("three starts logged the seed %d times, want once", got)
	}
	// Other modules warn that this test machine has no edge; the society
	// itself must log no problem.
	for _, p := range sm.logs.problems() {
		if strings.Contains(p, " agents: ") {
			t.Errorf("the society logged %q", p)
		}
	}
}
