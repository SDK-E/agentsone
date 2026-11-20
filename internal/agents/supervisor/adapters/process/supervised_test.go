// SPDX-License-Identifier: MIT

package process_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/agents/supervisor/adapters/process"
	"github.com/SDK-E/agentsone/internal/agents/supervisor/app"
	"github.com/SDK-E/agentsone/internal/agents/supervisor/domain"
	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/core/coretest"
)

// settableWall answers the network wall the test sets.
type settableWall struct {
	mu sync.Mutex
	w  domain.NetWall
}

func (f *settableWall) Check(context.Context) domain.NetWall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.w
}

func (f *settableWall) set(w domain.NetWall) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.w = w
}

// The bypass PM review found in the first fix of the always-on start: the
// wall breaks, then the runner crashes before the 5-minute wall recheck.
// With the real launcher under the real supervisor the crashed runner stays
// down, since the restart is the supervisor's and checks the wall afresh;
// once the wall holds, it starts again.
func TestLauncher_UnderTheSupervisorARunnerThatCrashesBehindABrokenWallStaysDown(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	fakes, ports := coretest.NewFakes()
	fakes.Agents.Add(core.Agent{Name: "founder", Kind: core.AgentAlwaysOn, IsRoot: true, Runner: "echo"})
	holds := domain.NetWall{Holds: true, Interfaces: []string{"eth0"}}
	broken := domain.JudgeNetWall([]string{"lo", "eth0"}, []domain.Route{{Iface: "eth0", Out: true, Desc: "0.0.0.0/0 via 172.17.0.1 dev eth0"}})
	wall := &settableWall{w: holds}
	out := fifo(t)
	l := &process.Launcher{Log: quiet}
	if err := l.Install(ctx, crashOnce(t, out)); err != nil {
		t.Fatal(err)
	}
	sup := app.New(app.Deps{Clock: fakes.Clock, IDs: fakes.IDs, Ports: ports, Launcher: l, Config: app.DefaultConfig(), Wall: wall, Log: quiet})
	exited := make(chan error, 4)
	l.Exited = func(name string, err error) {
		sup.RunnerExited(context.Background(), name, err)
		exited <- err
	}
	t.Cleanup(func() { _ = l.Stop(context.Background(), "founder") })
	sup.CheckWall(ctx)
	if err := sup.Run(ctx, "founder"); err != nil {
		t.Fatal(err)
	}
	if got := started(t, out); got != "crashed" {
		t.Fatalf("first run wrote %q, want crashed", got)
	}
	if err := receive(t, exited, "exit report"); err == nil {
		t.Fatal("the crash was reported as a clean exit")
	}

	wall.set(broken)
	fakes.Clock.Advance(domain.RestartFirstDelay)
	if err := sup.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if l.Running("founder") {
		t.Fatal("the crashed runner started again behind a broken wall")
	}

	wall.set(holds)
	fakes.Clock.Advance(time.Second)
	if err := sup.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	started(t, out) // the second run serves
	if !l.Running("founder") {
		t.Fatal("the runner did not start again once the wall held")
	}
}
