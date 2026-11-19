// SPDX-License-Identifier: MIT

package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/core/coretest"
	"github.com/SDK-E/agentsone/internal/events/adapters/memory"
	"github.com/SDK-E/agentsone/internal/events/app"
	"github.com/SDK-E/agentsone/internal/events/domain"
)

// fakeCatalog knows three types and rejects data that contains "bad".
type fakeCatalog struct{}

func (fakeCatalog) ScopeKind(t string) (string, bool) {
	switch t {
	case "agentsone.disk.usage.high":
		return "machine", true
	case "agentsone.agents.hired":
		return "team", true
	case "agentsone.agents.woke":
		return "agent", true
	}
	return "", false
}

func (fakeCatalog) ValidateData(t string, data json.RawMessage) error {
	if _, known := (fakeCatalog{}).ScopeKind(t); known && bytes.Contains(data, []byte("bad")) {
		return errors.Join(core.ErrInvalid, errors.New("bad data"))
	}
	return nil
}

// fakeUnits records unit control and can fail.
type fakeUnits struct {
	mu      sync.Mutex
	started []string
	stopped []string
	err     error
}

func (u *fakeUnits) Start(_ context.Context, e domain.Emitter) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.started = append(u.started, e.Unit)
	return u.err
}

func (u *fakeUnits) Stop(_ context.Context, e domain.Emitter) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.stopped = append(u.stopped, e.Unit)
	return u.err
}

// failingWaker answers err for every wake when err is set, else records it.
type failingWaker struct {
	*coretest.Waker
	err error
}

func (w *failingWaker) Wake(ctx context.Context, agent string, reason core.WakeReason, payload json.RawMessage) error {
	if w.err != nil {
		return w.err
	}
	return w.Waker.Wake(ctx, agent, reason, payload)
}

// taskRecorder keeps every NewTask it receives.
type taskRecorder struct {
	core.TaskService
	mu  sync.Mutex
	new []core.NewTask
}

func (r *taskRecorder) CreateTask(ctx context.Context, t core.NewTask) (core.Task, error) {
	r.mu.Lock()
	r.new = append(r.new, t)
	r.mu.Unlock()
	return r.TaskService.CreateTask(ctx, t)
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) { clear(p); return len(p), nil }

type fixture struct {
	svc      *app.Service
	fakes    *coretest.Fakes
	clock    *coretest.Clock
	units    *fakeUnits
	tasks    *taskRecorder
	waker    *failingWaker
	emitters *memory.Emitters
	triggers *memory.Triggers
	events   *memory.Events
}

// newFixture wires the service on in-memory repositories and the core fakes.
// The chart: founder > builder > intern, and outsider on its own.
func newFixture(t *testing.T) *fixture { return newFixtureWith(t, nil) }

// newFixtureWith lets a test swap dependencies before the service is built.
func newFixtureWith(t *testing.T, mutate func(*app.Deps)) *fixture {
	t.Helper()
	fakes, ports := coretest.NewFakes()
	for _, a := range []core.Agent{
		{Name: "founder", Kind: core.AgentAlwaysOn, IsRoot: true},
		{Name: "builder", Kind: core.AgentChat, Manager: "founder"},
		{Name: "intern", Kind: core.AgentChat, Manager: "builder"},
		{Name: "outsider", Kind: core.AgentChat},
	} {
		fakes.Agents.Add(a)
	}
	f := &fixture{
		fakes: fakes, clock: fakes.Clock, units: &fakeUnits{},
		tasks:    &taskRecorder{TaskService: fakes.Tasks},
		waker:    &failingWaker{Waker: fakes.Waker},
		emitters: memory.NewEmitters(), triggers: memory.NewTriggers(), events: memory.NewEvents(),
	}
	ports.Tasks, ports.Waker = f.tasks, f.waker
	deps := app.Deps{
		Events: f.events, Emitters: f.emitters, Triggers: f.triggers, Catalog: fakeCatalog{},
		Units: f.units, Clock: fakes.Clock, IDs: fakes.IDs, Ports: ports,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Entropy: zeroReader{},
	}
	if mutate != nil {
		mutate(&deps)
	}
	f.svc = app.New(deps)
	if err := f.svc.SeedBuiltins(t.Context()); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *fixture) wakes() []coretest.Wake { return f.fakes.Waker.Wakes() }

func (f *fixture) auditActions() []string {
	var out []string
	for _, e := range f.fakes.Audit.Entries() {
		out = append(out, e.Action)
	}
	return out
}

func asAgent(name string) context.Context {
	return core.WithCaller(context.Background(), core.Caller{Kind: core.CallerAgent, Name: name, UID: 1000})
}

func asRootAgent(name string) context.Context {
	return core.WithCaller(context.Background(), core.Caller{Kind: core.CallerAgent, Name: name, IsRoot: true})
}

func asHuman(name string) context.Context {
	return core.WithCaller(context.Background(), core.Caller{Kind: core.CallerHuman, Name: name, UID: 1001})
}

func asSystem() context.Context { return context.Background() }

func diskEvent() domain.Draft {
	return domain.Draft{
		Type: "agentsone.disk.usage.high", Source: "/emitters/disk", Subject: "/var",
		Data: json.RawMessage(`{"mount":"/var"}`),
	}
}

func custom(typ string) domain.Draft {
	return domain.Draft{Type: typ, Data: json.RawMessage(`{"n":1}`)}
}

var window = time.Minute
