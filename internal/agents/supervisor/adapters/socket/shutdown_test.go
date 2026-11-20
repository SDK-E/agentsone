// SPDX-License-Identifier: MIT

package socket_test

import (
	"context"
	"os"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/agents/supervisor/adapters/socket"
	"github.com/SDK-E/agentsone/internal/agents/supervisor/app"
	"github.com/SDK-E/agentsone/internal/agents/supervisor/domain"
	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/core/coretest"
)

// runsHeard records the run states the society was told, in order.
type runsHeard struct {
	mu     sync.Mutex
	states []app.RunState
}

func (r *runsHeard) RunnerState(_ context.Context, _ string, st app.RunState) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.states = append(r.states, st)
	return nil
}

func (r *runsHeard) TurnEnded(context.Context, string, time.Time) error { return nil }

func (r *runsHeard) heard() []app.RunState {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.states)
}

// The daemon closes its stores after the runner sockets. A runner still
// connected when the sockets shut must have been reported stopped by the time
// Shutdown returns, never after it, when the society could no longer take it.
func TestServer_ShutdownReturnsAfterTheSocietyHeardEveryRunnerStop(t *testing.T) {
	t.Parallel()
	dir, err := os.MkdirTemp("", "ao") // short socket path
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	fakes, ports := coretest.NewFakes()
	fakes.Agents.Add(core.Agent{Name: "founder", Kind: core.AgentAlwaysOn, IsRoot: true})
	c := &collect{sums: map[string]app.Summary{}}
	runs := &runsHeard{}
	inner := &startLauncher{started: make(chan string, 4)}
	srv := &socket.Server{Dir: dir, HelloDeadline: func() <-chan struct{} { return make(chan struct{}) }}
	srv.Chown = func(string, int) error { return nil }
	launcher := &socket.Launcher{Inner: inner, Server: srv, Ctx: t.Context()}
	sup := app.New(app.Deps{Clock: fakes.Clock, IDs: fakes.IDs, Ports: ports, Launcher: launcher, Identity: c, Output: c, Summaries: c, Usage: c, Runs: runs, Config: app.DefaultConfig(), Wall: holdingWall{}})
	sup.CheckWall(t.Context())
	srv.Supervisor = sup
	if err := launcher.Install(t.Context(), domain.UnitSpec{Agent: "founder", Account: domain.AccountFor("founder", true)}); err != nil {
		t.Fatal(err)
	}
	if err := sup.Run(t.Context(), "founder"); err != nil {
		t.Fatal(err)
	}
	<-inner.started
	r := dialRunner(t, srv.Path("founder"))
	r.send(`{"jsonrpc":"2.0","id":1,"method":"hello","params":{"protocolVersions":["1.0"],"runner":{"name":"peer","version":"1.0.0"},"capabilities":{"streaming":true,"cancel":true,"compaction":true,"resume":true}}}`)
	if w := r.read(); w["result"] == nil {
		t.Fatalf("first line = %v, want the welcome", w)
	}
	r.send(`{"jsonrpc":"2.0","method":"status","params":{"state":"idle"}}`)
	// Lines are handled in order: once this is answered, the status was taken.
	r.send(`{"jsonrpc":"2.0","id":2,"method":"ping","params":{}}`)
	r.read()

	if err := srv.Shutdown(); err != nil {
		t.Fatal(err)
	}

	got := runs.heard()
	if len(got) == 0 || got[len(got)-1] != app.RunStopped {
		t.Fatalf("society heard %v when Shutdown returned, want the last to be %q", got, app.RunStopped)
	}
}
