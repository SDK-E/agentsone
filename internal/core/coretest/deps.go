// SPDX-License-Identifier: MIT

package coretest

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/store"
)

// Fakes are the concrete fakes behind a *core.Ports, for inspection.
type Fakes struct {
	Clock     *Clock
	IDs       core.IDs
	Agents    *Agents
	Waker     *Waker
	Events    *Events
	Tasks     *Tasks
	Audit     *Audit
	Live      *Live
	Budget    *Budget
	Runners   *Runners
	Bus       *Bus
	Freezer   *Freezer
	Tokens    *Tokens
	Templates *Templates
	Profiles  *Profiles
	Chat      *Chat
	Startup   *Startup
	Seed      *Seed
	// GatewayTokens is the fake gateway token minter.
	GatewayTokens *GatewayTokens
}

// NewFakes returns fresh fakes and Ports wired to them.
func NewFakes() (*Fakes, *core.Ports) {
	clock := NewClock(Epoch)
	ids := NewIDs()
	f := &Fakes{
		Clock:     clock,
		IDs:       ids,
		Agents:    NewAgents(),
		Waker:     NewWaker(),
		Events:    NewEvents(clock, ids),
		Tasks:     NewTasks(clock, ids),
		Audit:     NewAudit(),
		Live:      NewLive(clock),
		Budget:    NewBudget(),
		Runners:   NewRunners(),
		Bus:       NewBus(clock, ids),
		Freezer:   NewFreezer(),
		Tokens:    NewTokens(clock, ids),
		Templates: NewTemplates(),
		Profiles:  NewProfiles(),
		Chat:      NewChat(ids),
		Startup:   NewStartup(),
		Seed:      NewSeed(),

		GatewayTokens: NewGatewayTokens(),
	}
	return f, f.Ports()
}

// Ports returns Ports backed by f.
func (f *Fakes) Ports() *core.Ports {
	return &core.Ports{
		Agents: f.Agents, Waker: f.Waker, Events: f.Events, Subscribe: f.Events,
		Tasks: f.Tasks, Audit: f.Audit, Live: f.Live, Budget: f.Budget, Runners: f.Runners,
		Bus: f.Bus, Freezer: f.Freezer, Tokens: f.Tokens, Templates: f.Templates,
		Profiles: f.Profiles, Chat: f.Chat, Startup: f.Startup, Seed: f.Seed,
		GatewayTokens: f.GatewayTokens,
	}
}

// NewPorts returns Ports backed by fresh fakes.
func NewPorts() *core.Ports {
	_, p := NewFakes()
	return p
}

// NewDeps returns Deps for a module test: folders under t.TempDir, real
// SQLite stores closed at cleanup, a discarded log and fake ports.
func NewDeps(t testing.TB) (core.Deps, *Fakes) {
	t.Helper()
	cfg := testConfig(t.TempDir())
	fakes, ports := NewFakes()
	return core.Deps{
		Config: &cfg,
		Log:    slog.New(slog.DiscardHandler),
		Clock:  fakes.Clock,
		IDs:    fakes.IDs,
		Stores: openStores(t, cfg),
		Ports:  ports,
	}, fakes
}

func testConfig(dir string) core.Config {
	cfg := core.DefaultConfig()
	cfg.StateDir = filepath.Join(dir, "state")
	cfg.RuntimeDir = filepath.Join(dir, "run")
	cfg.AgentsHome = filepath.Join(dir, "agents")
	cfg.API.Addr = "127.0.0.1:0"
	return cfg
}

func openStores(t testing.TB, cfg core.Config) core.Stores {
	t.Helper()
	if err := os.MkdirAll(cfg.StateDir, 0o750); err != nil {
		t.Fatalf("create state folder: %v", err)
	}
	open := func(name string) *store.DB {
		db, err := store.Open(context.Background(), cfg.DBPath(name))
		if err != nil {
			t.Fatalf("open %s store: %v", name, err)
		}
		t.Cleanup(func() {
			if err := db.Close(); err != nil {
				t.Errorf("close %s store: %v", name, err)
			}
		})
		return db
	}
	return core.Stores{Core: open("core"), Bus: open("bus"), Memory: open("memory"), Audit: open("audit")}
}
