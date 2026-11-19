// SPDX-License-Identifier: MIT

package app_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/core/coretest"
	"github.com/SDK-E/agentsone/internal/sched/adapters/memory"
	"github.com/SDK-E/agentsone/internal/sched/app"
)

// env is a small society and the services under test, on fakes.
//
//	founder (root)
//	├── builder ── scribe
//	├── keeper
//	└── peer
type env struct {
	fakes     *coretest.Fakes
	ports     *core.Ports
	tasks     *app.Tasks
	schedules *app.Schedules
}

func newEnv(t *testing.T) *env {
	t.Helper()
	fakes, ports := coretest.NewFakes()
	add := func(name, manager string, root bool) {
		fakes.Agents.Add(core.Agent{Name: name, Kind: core.AgentChat, Manager: manager, IsRoot: root, Runner: "echo"})
	}
	add("founder", "", true)
	add("builder", "founder", false)
	add("scribe", "builder", false)
	add("keeper", "founder", false)
	add("peer", "founder", false)
	deps := app.Deps{Clock: fakes.Clock, IDs: fakes.IDs, Ports: ports, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	e := &env{
		fakes:     fakes,
		ports:     ports,
		tasks:     app.NewTasks(deps, memory.NewTasks()),
		schedules: app.NewSchedules(deps, memory.NewSchedules()),
	}
	ports.Tasks = e.tasks
	return e
}

// asAgent is a context for a request made by an agent.
func asAgent(name string) context.Context {
	return core.WithCaller(context.Background(), core.Caller{Kind: core.CallerAgent, Name: name, UID: 1000})
}

// asHuman is a context for a request made by a signed-in human.
func asHuman(name string) context.Context {
	return core.WithCaller(context.Background(), core.Caller{Kind: core.CallerHuman, Name: name, UID: -1})
}

// freeze activates a freeze such as "agent:scribe", "team:builder" or "all".
func (e *env) freeze(t *testing.T, scope string) {
	t.Helper()
	s, err := core.ParseFreezeScope(scope)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.fakes.Freezer.Freeze(context.Background(), s); err != nil {
		t.Fatal(err)
	}
}

// failingWaker is a Waker that fails with err, as an agent runner that is down would.
type failingWaker struct{ err error }

func (w failingWaker) Wake(context.Context, string, core.WakeReason, json.RawMessage) error {
	return w.err
}

// failingAudit is an AuditLog that cannot write.
type failingAudit struct{ err error }

func (a failingAudit) Append(context.Context, core.AuditEntry) error { return a.err }

func (e *env) auditActions() []string {
	var out []string
	for _, a := range e.fakes.Audit.Entries() {
		out = append(out, a.Action)
	}
	return out
}

func (e *env) wakedAgents() []string {
	var out []string
	for _, w := range e.fakes.Waker.Wakes() {
		out = append(out, w.Agent)
	}
	return out
}

func payloadOf(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("payload %s: %v", raw, err)
	}
	return m
}
