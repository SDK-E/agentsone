// SPDX-License-Identifier: MIT

package socket_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/SDK-E/agentsone/internal/agents/supervisor/adapters/socket"
	"github.com/SDK-E/agentsone/internal/agents/supervisor/app"
	"github.com/SDK-E/agentsone/internal/agents/supervisor/domain"
	"github.com/SDK-E/agentsone/internal/contract/contracttest"
	"github.com/SDK-E/agentsone/internal/contract/eventdata"
	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/core/coretest"
)

// A runner that paused its turn for budget, over the real socket and the
// real connection: when the budget is back, the supervisor publishes the
// event and the runner gets an event wake, on the paused turn, whose payload
// matches the runner contract.
func TestServer_RestoredBudgetResumesThePausedTurn(t *testing.T) {
	t.Parallel()
	dir, err := os.MkdirTemp("", "ao") // B5c: short socket path
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	fakes, ports := coretest.NewFakes()
	fakes.Agents.Add(core.Agent{Name: "founder", Kind: core.AgentAlwaysOn, IsRoot: true})
	c := &collect{sums: map[string]app.Summary{}}
	inner := &startLauncher{started: make(chan string, 4)}
	srv := &socket.Server{Dir: dir, HelloDeadline: func() <-chan struct{} { return make(chan struct{}) }}
	srv.Chown = func(string, int) error { return nil }
	launcher := &socket.Launcher{Inner: inner, Server: srv, Ctx: t.Context()}
	t.Cleanup(func() { _ = srv.Shutdown() })
	sup := app.New(app.Deps{Clock: fakes.Clock, IDs: fakes.IDs, Ports: ports, Launcher: launcher, Identity: c, Output: c, Summaries: c, Usage: c, Config: app.DefaultConfig(), Wall: holdingWall{}})
	sup.CheckWall(t.Context())
	srv.Supervisor = sup
	if err := launcher.Install(t.Context(), domain.UnitSpec{Agent: "founder", Account: domain.AccountFor("founder", true)}); err != nil {
		t.Fatal(err)
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
	turn := held["params"].(map[string]any)["turnId"].(string)
	r.send(`{"jsonrpc":"2.0","id":1,"result":{}}`)
	r.send(`{"jsonrpc":"2.0","method":"status","params":{"state":"working","turnId":"` + turn + `"}}`)
	r.send(`{"jsonrpc":"2.0","method":"profile.update","params":{"turnId":"` + turn + `","line":"Goal: keep this machine useful. Now: waiting for budget."}}`)
	r.send(`{"jsonrpc":"2.0","method":"status","params":{"state":"idle","turnId":"` + turn + `","pausedTurnId":"` + turn + `"}}`)
	// Lines are handled in order: once this request is answered, the pause is known.
	r.send(`{"jsonrpc":"2.0","id":1000,"method":"ping","params":{}}`)
	r.read()

	fakes.Budget.Set(core.Budget{Agent: "founder", AllocatedMicros: 1000, RemainingMicros: 0})
	if err := sup.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	fakes.Budget.Set(core.Budget{Agent: "founder", AllocatedMicros: 1000, RemainingMicros: 700})
	if err := sup.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}

	wake := r.read()
	params, _ := wake["params"].(map[string]any)
	if wake["method"] != "wake" || params["reason"] != "event" || params["turnId"] != turn {
		t.Fatalf("request = %v, want an event wake on paused turn %s", wake, turn)
	}
	payload, _ := json.Marshal(params["payload"])
	if err := contracttest.ValidateWakePayload("event", payload); err != nil {
		t.Errorf("wake payload does not match the runner contract: %v", err)
	}
	var published int
	for _, e := range fakes.Events.Published() {
		if e.Type == eventdata.TypeAgentsBudgetRestored {
			published++
		}
	}
	if published != 1 {
		t.Errorf("%d budget.restored events, want 1", published)
	}
	var resumed int
	for _, e := range fakes.Audit.Entries() {
		if e.Action == app.ActionBudgetResume && e.Target == "agent:founder" {
			resumed++
		}
	}
	if resumed != 1 {
		t.Errorf("%d resume audit records, want 1", resumed)
	}
	r.send(`{"jsonrpc":"2.0","id":2,"result":{}}`)
	_ = context.Background()
}
