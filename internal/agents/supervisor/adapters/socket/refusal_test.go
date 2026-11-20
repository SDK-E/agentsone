// SPDX-License-Identifier: MIT

package socket_test

import (
	"context"
	"encoding/json"
	"os"
	"runtime"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/SDK-E/agentsone/internal/agents/supervisor/adapters/socket"
	"github.com/SDK-E/agentsone/internal/agents/supervisor/app"
	"github.com/SDK-E/agentsone/internal/agents/supervisor/domain"
	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/core/coretest"
)

// states keeps what the supervisor tells the society about a runner.
type states struct {
	mu    sync.Mutex
	heard []string
}

func (s *states) add(line string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.heard = append(s.heard, line)
}
func (s *states) RunnerFailed(_ context.Context, agent, reason string) error {
	s.add("failed " + agent + ": " + reason)
	return nil
}
func (s *states) RunnerRunning(_ context.Context, agent string) error {
	s.add("running " + agent)
	return nil
}
func (s *states) RunnerCleared(_ context.Context, agent string) error {
	s.add("cleared " + agent)
	return nil
}
func (s *states) all() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.heard)
}

// A runner that answers a wake with -32602 over the real socket and the real
// connection: the refusal is audited and shown once however often it
// repeats, and the next accepted wake clears it.
func TestServer_RunnerRefusingAWakeIsLoudOnce(t *testing.T) {
	t.Parallel()
	dir, err := os.MkdirTemp("", "ao") // B5c: short socket path
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	fakes, ports := coretest.NewFakes()
	fakes.Agents.Add(core.Agent{Name: "founder", Kind: core.AgentAlwaysOn, IsRoot: true})
	c := &collect{sums: map[string]app.Summary{}}
	st := &states{}
	inner := &startLauncher{started: make(chan string, 4)}
	srv := &socket.Server{Dir: dir, HelloDeadline: func() <-chan struct{} { return make(chan struct{}) }}
	srv.Chown = func(string, int) error { return nil }
	launcher := &socket.Launcher{Inner: inner, Server: srv, Ctx: t.Context()}
	t.Cleanup(func() { _ = srv.Shutdown() })
	sup := app.New(app.Deps{Clock: fakes.Clock, IDs: fakes.IDs, Ports: ports, Launcher: launcher, Identity: c, Output: c, Summaries: c, Usage: c, Config: app.DefaultConfig(), Wall: holdingWall{}, States: st})
	sup.CheckWall(t.Context())
	srv.Supervisor = sup
	if err := launcher.Install(t.Context(), domain.UnitSpec{Agent: "founder", Account: domain.AccountFor("founder", true)}); err != nil {
		t.Fatal(err)
	}
	refusals := func() int {
		n := 0
		for _, e := range fakes.Audit.Entries() {
			if e.Action == app.ActionWakeRefused {
				n++
			}
		}
		return n
	}

	if err := sup.Wake(t.Context(), "founder", core.WakeChat, json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	<-inner.started
	r := dialRunner(t, srv.Path("founder"))
	r.send(`{"jsonrpc":"2.0","id":1,"method":"hello","params":{"protocolVersions":["1.0"],"runner":{"name":"peer","version":"1.0.0"},"capabilities":{"streaming":true,"cancel":true,"compaction":true,"resume":true}}}`)
	r.read()
	r.send(`{"jsonrpc":"2.0","method":"status","params":{"state":"idle"}}`)
	held := r.read()
	if held["method"] != "wake" {
		t.Fatalf("held wake = %v", held)
	}
	r.send(`{"jsonrpc":"2.0","id":1,"error":{"code":-32602,"message":"Invalid params"}}`)
	deadline := time.Now().Add(waitFor)
	for refusals() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the refused held wake was not audited within 5s")
		}
		runtime.Gosched()
	}

	// The same refusal again, as a direct wake: no second record.
	errc := make(chan error, 1)
	go func() { errc <- sup.Wake(context.Background(), "founder", core.WakeChat, json.RawMessage(`{}`)) }()
	if again := r.read(); again["method"] != "wake" {
		t.Fatalf("second wake = %v", again)
	}
	r.send(`{"jsonrpc":"2.0","id":2,"error":{"code":-32602,"message":"Invalid params"}}`)
	if err := <-errc; err == nil {
		t.Fatal("Wake() = nil, want the runner's refusal")
	}
	if n := refusals(); n != 1 {
		t.Fatalf("%d audit records for two identical refusals, want 1", n)
	}
	reason := "The runner refused a chat wake: error -32602, Invalid params."
	if diff := cmp.Diff([]string{"running founder", "failed founder: " + reason}, st.all()); diff != "" {
		t.Fatalf("society heard (-want +got):\n%s", diff)
	}

	// An accepted wake clears it.
	go func() { errc <- sup.Wake(context.Background(), "founder", core.WakeChat, json.RawMessage(`{}`)) }()
	if third := r.read(); third["method"] != "wake" {
		t.Fatalf("third wake = %v", third)
	}
	r.send(`{"jsonrpc":"2.0","id":3,"result":{}}`)
	if err := <-errc; err != nil {
		t.Fatalf("Wake() = %v, want accepted", err)
	}
	if diff := cmp.Diff([]string{"running founder", "failed founder: " + reason, "running founder"}, st.all()); diff != "" {
		t.Errorf("society heard (-want +got):\n%s", diff)
	}
}
