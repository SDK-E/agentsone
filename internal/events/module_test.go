// SPDX-License-Identifier: MIT

package events_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/contract/api"
	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/core/coretest"
	"github.com/SDK-E/agentsone/internal/core/moduletest"
	"github.com/SDK-E/agentsone/internal/events"
)

func TestModuleOwnsItsTags(t *testing.T) {
	deps, _ := coretest.NewDeps(t)
	m := events.New(deps)
	if m.Name() != "events" {
		t.Fatalf("name %q", m.Name())
	}
	moduletest.AssertOwnsTags(t, m, "events", "triggers")
}

// boot runs the lifecycle steps the daemon runs, up to Start.
func boot(t *testing.T) (*events.Module, core.Deps) {
	t.Helper()
	deps, _ := coretest.NewDeps(t)
	m := events.New(deps)
	m.Provide(deps.Ports)
	if err := m.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := m.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := m.Stop(context.Background()); err != nil {
			t.Errorf("Stop() error = %v", err)
		}
	})
	return m, deps
}

func TestModule_StopIsSafeWithoutStart(t *testing.T) {
	deps, _ := coretest.NewDeps(t)
	if err := events.New(deps).Stop(t.Context()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
}

func TestModule_RefusesAnEmittersTableThatCannotWork(t *testing.T) {
	deps, _ := coretest.NewDeps(t)
	cfg, err := core.ParseConfig([]byte("[emitters.disk]\nthreshold_percent = 500\n"))
	if err != nil {
		t.Fatal(err)
	}
	deps.Config = &cfg

	m := events.New(deps)

	if err := m.Migrate(t.Context()); err == nil {
		t.Fatal("Migrate() error = nil, want the bad [emitters] table reported")
	}
	if err := m.Start(t.Context()); err == nil {
		t.Fatal("Start() error = nil, want the bad [emitters] table reported")
	}
}

// The disk source runs inside the daemon and publishes through the bus: with
// a threshold of 1 percent any real file system is high.
func TestModule_RunsTheDiskSourceInProcess(t *testing.T) {
	deps, _ := coretest.NewDeps(t)
	dir := t.TempDir()
	table := filepath.Join(dir, "mounts")
	if err := os.WriteFile(table, []byte("/dev/x "+dir+" ext4 rw 0 0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := core.ParseConfig([]byte("[emitters.disk]\nthreshold_percent = 1\nmargin_percent = 0\nmounts_file = '" + table + "'\n"))
	if err != nil {
		t.Fatal(err)
	}
	deps.Config = &cfg
	m := events.New(deps)
	m.Provide(deps.Ports)
	if err := m.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	live, err := deps.Ports.Subscribe.Subscribe(t.Context(), core.EventFilter{Types: []string{"agentsone.disk."}})
	if err != nil {
		t.Fatal(err)
	}

	if err := m.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Stop(context.Background()) }()

	select {
	case e := <-live:
		if e.Type != "agentsone.disk.usage.high" || e.Source != "/emitters/disk" || e.Subject != dir || e.Actor != "system:daemon" {
			t.Fatalf("event = %+v", e)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no disk event within 10 seconds")
	}
}

func TestModule_RunsTheLoadSourceInProcess(t *testing.T) {
	deps, _ := coretest.NewDeps(t)
	proc := t.TempDir()
	stat := "cpu  100 0 100 800 0 0 0 0 0 0\n"
	mem := "MemTotal:        1000000 kB\nMemAvailable:      50000 kB\n"
	for name, body := range map[string]string{"stat": stat, "meminfo": mem} {
		if err := os.WriteFile(filepath.Join(proc, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cfg, err := core.ParseConfig([]byte("[emitters.load]\ninterval = '1s'\nwindow = '1s'\nproc_dir = '" + proc + "'\n"))
	if err != nil {
		t.Fatal(err)
	}
	deps.Config = &cfg
	m := events.New(deps)
	m.Provide(deps.Ports)
	if err := m.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	live, err := deps.Ports.Subscribe.Subscribe(t.Context(), core.EventFilter{Types: []string{"agentsone.load."}})
	if err != nil {
		t.Fatal(err)
	}

	if err := m.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Stop(context.Background()) }()

	select {
	case e := <-live:
		if e.Type != "agentsone.load.memory.high" || e.Source != "/emitters/load" || e.Actor != "system:daemon" {
			t.Fatalf("event = %+v", e)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no load event within 10 seconds")
	}
}

func TestModule_SkipsTheLoadSourceWhereThereIsNoProc(t *testing.T) {
	deps, _ := coretest.NewDeps(t)
	cfg, err := core.ParseConfig([]byte("[emitters.load]\nproc_dir = '" + t.TempDir() + "'\n"))
	if err != nil {
		t.Fatal(err)
	}
	deps.Config = &cfg
	m := events.New(deps)
	m.Provide(deps.Ports)
	if err := m.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}

	if err := m.Start(t.Context()); err != nil {
		t.Fatalf("Start() error = %v, want the daemon to start without /proc", err)
	}
	if err := m.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestModule_SeedsTheBuiltinEmittersAndServesThem(t *testing.T) {
	m, _ := boot(t)
	if err := m.Start(t.Context()); err != nil {
		t.Fatalf("second Start() error = %v, want it to be safe to run again", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/v1/emitters", nil)
	rec := httptest.NewRecorder()

	moduletest.Mount(m).ServeHTTP(rec, req)

	var list api.EmitterList
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil || rec.Code != http.StatusOK || len(list.Items) != 9 {
		t.Fatalf("status %d, %d emitters, %v: %s", rec.Code, len(list.Items), err, rec.Body)
	}
}

func TestModule_ProvidesThePublishAndSubscribePorts(t *testing.T) {
	_, deps := boot(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	live, err := deps.Ports.Subscribe.Subscribe(ctx, core.EventFilter{Types: []string{"agentsone.custom."}})
	if err != nil {
		t.Fatal(err)
	}

	stored, err := deps.Ports.Events.Publish(ctx, core.Event{
		Type: "agentsone.custom.thing", Source: "/daemon", Data: json.RawMessage(`{"n":1}`),
	})

	if err != nil || stored.Sequence == "" || stored.Actor != "system:daemon" {
		t.Fatalf("Publish() = %+v, %v", stored, err)
	}
	if got := <-live; got.ID != stored.ID {
		t.Fatalf("subscriber got %s, want %s", got.ID, stored.ID)
	}
}
