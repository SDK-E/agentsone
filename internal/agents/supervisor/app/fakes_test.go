// SPDX-License-Identifier: MIT

package app_test

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/agents/supervisor/app"
	"github.com/SDK-E/agentsone/internal/agents/supervisor/domain"
	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/core/coretest"
	"github.com/SDK-E/agentsone/runners/sdk/protocol"
)

type launcher struct {
	mu        sync.Mutex
	installed map[string]domain.UnitSpec
	running   map[string]bool
	restarts  map[string]int
	startErr  error
}

func newLauncher() *launcher {
	return &launcher{installed: map[string]domain.UnitSpec{}, running: map[string]bool{}, restarts: map[string]int{}}
}

func (l *launcher) Install(_ context.Context, u domain.UnitSpec) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.installed[u.Agent] = u
	return nil
}
func (l *launcher) Start(_ context.Context, a string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.startErr != nil {
		return l.startErr
	}
	l.running[a] = true
	return nil
}
func (l *launcher) Stop(_ context.Context, a string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.running[a] = false
	return nil
}
func (l *launcher) Restart(_ context.Context, a string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.running[a] = true
	l.restarts[a]++
	return nil
}
func (l *launcher) Remove(_ context.Context, a string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.installed, a)
	return nil
}
func (l *launcher) Mode(a string) domain.LimitMode {
	l.mu.Lock()
	defer l.mu.Unlock()
	if u, ok := l.installed[a]; ok && !u.Account.IsRoot {
		return domain.LimitsHard
	}
	return domain.LimitsNone
}
func (l *launcher) isRunning(a string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.running[a]
}
func (l *launcher) unit(a string) (domain.UnitSpec, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	u, ok := l.installed[a]
	return u, ok
}

type accounts struct {
	mu        sync.Mutex
	created   map[string]domain.Account
	locked    map[string]bool
	rootHomes map[string]bool
}

func (a *accounts) EnsureRootHome(_ context.Context, acc domain.Account) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.rootHomes == nil {
		a.rootHomes = map[string]bool{}
	}
	a.rootHomes[acc.Home] = true
	return nil
}

func (a *accounts) Ensure(_ context.Context, acc domain.Account) (int, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.created[acc.User] = acc
	return acc.UID, nil
}
func (a *accounts) Lock(_ context.Context, acc domain.Account) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.locked[acc.User] = true
	return nil
}

type identity struct{ agents *coretest.Agents }

func (i identity) Identity(ctx context.Context, name string) (app.AgentIdentity, error) {
	a, err := i.agents.GetAgent(ctx, name)
	if err != nil {
		return app.AgentIdentity{}, err
	}
	return app.AgentIdentity{Agent: a, Team: name, Charter: "Charter.", OneLine: "Goal: g. Now: n.", Goals: []string{"Keep going"}}, nil
}

type sink struct {
	mu     sync.Mutex
	chunks []app.OutputChunk
	usage  []app.Usage
	states []string
}

func (s *sink) Output(_ context.Context, _ string, c app.OutputChunk) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.chunks = append(s.chunks, c)
	return nil
}
func (s *sink) RecordUsage(_ context.Context, _ string, u app.Usage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.usage = append(s.usage, u)
	return nil
}

// RunnerFailed and RunnerRunning record what the society would hear, as
// "failed <agent>: <reason>" and "running <agent>".
func (s *sink) RunnerFailed(_ context.Context, agent, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.states = append(s.states, "failed "+agent+": "+reason)
	return nil
}
func (s *sink) RunnerRunning(_ context.Context, agent string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.states = append(s.states, "running "+agent)
	return nil
}
func (s *sink) RunnerCleared(_ context.Context, agent string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.states = append(s.states, "cleared "+agent)
	return nil
}
func (s *sink) heard() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.states)
}

type summaries struct {
	mu sync.Mutex
	m  map[string]app.Summary
}

func (s *summaries) Load(_ context.Context, a string) (app.Summary, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.m[a], nil
}
func (s *summaries) Save(_ context.Context, a string, v app.Summary) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[a] = v
	return nil
}

// call is one request the supervisor sent to the fake runner.
type call struct {
	method string
	params json.RawMessage
}

// fakeConn is the runner end as the app sees it: it records requests and
// answers sleep with a summary and everything else with an empty result.
// Like the real connection it refuses calls until it is marked ready, which
// the read loop does only after Status returns (rule H8).
type fakeConn struct {
	calls     chan call
	arrivals  chan string // "wait" for each WaitReady, "refused <method>" for each refused Call
	ready     chan struct{}
	readyOnce sync.Once
	closed    chan struct{}
	once      sync.Once
}

var errFakeNotReady = errors.New("runner has not reported its first status")

func newFakeConn() *fakeConn {
	return &fakeConn{calls: make(chan call, 32), arrivals: make(chan string, 32), ready: make(chan struct{}), closed: make(chan struct{})}
}

func (c *fakeConn) Call(_ context.Context, method string, params, result any) error {
	select {
	case <-c.ready:
	default:
		c.arrive("refused " + method)
		return errFakeNotReady
	}
	raw, _ := json.Marshal(params)
	c.calls <- call{method, raw}
	if method == protocol.MethodSleep && result != nil {
		return json.Unmarshal([]byte(`{"summary":"Chatted with alice."}`), result)
	}
	return nil
}

// arrive records a, unless no test is reading arrivals and the buffer is full.
func (c *fakeConn) arrive(a string) {
	select {
	case c.arrivals <- a:
	default:
	}
}

func (c *fakeConn) WaitReady(ctx context.Context) error {
	c.arrive("wait")
	select {
	case <-c.ready:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *fakeConn) markReady() { c.readyOnce.Do(func() { close(c.ready) }) }

// markReadyAfter plays the read loop between Status returning and the
// connection marking itself ready: it waits until n calls have either
// started waiting for readiness or been refused, fails on a refusal (that
// request never reaches the runner), then marks the connection ready.
func (c *fakeConn) markReadyAfter(t *testing.T, n int) {
	t.Helper()
	for range n {
		select {
		case a := <-c.arrivals:
			if a != "wait" {
				t.Fatalf("%s: sent before the connection was ready, so the runner never gets it", a)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("no request to the runner within 5s")
		}
	}
	c.markReady()
}

func (c *fakeConn) Close() error { c.once.Do(func() { close(c.closed) }); return nil }

func (c *fakeConn) next(t *testing.T) call {
	t.Helper()
	select {
	case got := <-c.calls:
		return got
	case <-time.After(5 * time.Second):
		t.Fatal("no request to the runner within 5s")
		return call{}
	}
}

type echoAdapter struct{}

func (echoAdapter) Info() core.RunnerInfo {
	return core.RunnerInfo{Name: "echo", Version: "1.0.0", Source: "builtin"}
}
func (echoAdapter) Exec(_ context.Context, a core.Agent, socket string) (core.ExecSpec, error) {
	return core.ExecSpec{Path: "/usr/lib/agentsone/runners/echo", Args: []string{"--agent", a.Name}, Env: []string{"ECHO_SOCKET=" + socket}}, nil
}

type world struct {
	fakes     *coretest.Fakes
	launcher  *launcher
	accounts  *accounts
	sink      *sink
	summaries *summaries
	wall      *fakeWall
	logs      *logLines
	ports     *core.Ports
	sup       *app.Supervisor
}

// newWorld is a supervisor whose network wall was checked and holds.
func newWorld(t *testing.T) *world {
	t.Helper()
	w := newUncheckedWorld(t)
	w.sup.CheckWall(t.Context())
	return w
}

// newUncheckedWorld is a supervisor before the daemon checked the wall.
func newUncheckedWorld(t *testing.T) *world {
	t.Helper()
	fakes, ports := coretest.NewFakes()
	fakes.Runners.Register(echoAdapter{})
	fakes.Agents.Add(core.Agent{Name: "founder", Kind: core.AgentAlwaysOn, IsRoot: true, Runner: "echo"})
	fakes.Agents.Add(core.Agent{Name: "mira", Kind: core.AgentChat, Manager: "founder", Runner: "echo", UID: 200001})
	fakes.Agents.Add(core.Agent{Name: "watcher", Kind: core.AgentWaker, Manager: "founder", Runner: "echo", UID: 200002})
	w := &world{
		fakes: fakes, launcher: newLauncher(),
		accounts: &accounts{created: map[string]domain.Account{}, locked: map[string]bool{}},
		sink:     &sink{}, summaries: &summaries{m: map[string]app.Summary{}},
		wall:  &fakeWall{w: domain.NetWall{Holds: true, Interfaces: []string{"eth0"}}},
		logs:  &logLines{},
		ports: ports,
	}
	w.sup = app.New(app.Deps{
		Clock: fakes.Clock, IDs: fakes.IDs, Ports: ports, Launcher: w.launcher, Accounts: w.accounts,
		Identity: identity{fakes.Agents}, Output: w.sink, Summaries: w.summaries, Usage: w.sink,
		Config: app.DefaultConfig(), States: w.sink, Wall: w.wall, Log: slog.New(w.logs),
	})
	return w
}

// fakeWall answers what the test sets and counts the checks.
type fakeWall struct {
	mu     sync.Mutex
	w      domain.NetWall
	checks int
}

func (f *fakeWall) Check(context.Context) domain.NetWall {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.checks++
	return f.w
}
func (f *fakeWall) set(w domain.NetWall) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.w = w
}
func (f *fakeWall) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.checks
}

// logLines keeps each log record as "<LEVEL> <message>".
type logLines struct {
	mu    sync.Mutex
	lines []string
}

func (l *logLines) Enabled(context.Context, slog.Level) bool { return true }
func (l *logLines) Handle(_ context.Context, r slog.Record) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, r.Level.String()+" "+r.Message)
	return nil
}
func (l *logLines) WithAttrs([]slog.Attr) slog.Handler { return l }
func (l *logLines) WithGroup(string) slog.Handler      { return l }
func (l *logLines) all() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.lines)
}

func (w *world) actions() []string {
	var out []string
	for _, e := range w.fakes.Audit.Entries() {
		out = append(out, e.Action+" "+e.Target)
	}
	return out
}

var helloParams = protocol.HelloParams{
	ProtocolVersions: []protocol.ProtocolVersion{"1.0"},
	Runner:           protocol.RunnerInfo{Name: "echo", Version: "1.0.0"},
	Capabilities:     protocol.RunnerCapabilities{Streaming: true, Cancel: true, Compaction: true, Resume: true},
}

// connect plays a runner that connects and finishes the handshake.
func (w *world) connect(t *testing.T, agent string) (*app.Handler, *fakeConn, protocol.HelloResult) {
	t.Helper()
	h := w.sup.Connect(agent)
	conn := newFakeConn()
	h.Attach(conn)
	res, err := h.Hello(t.Context(), "1.0", helloParams)
	if err != nil {
		t.Fatalf("Hello() error = %v", err)
	}
	if err := h.Status(t.Context(), protocol.StatusParams{State: "idle"}); err != nil {
		t.Fatalf("first Status(idle) error = %v", err)
	}
	conn.markReady() // the read loop, once Status has returned
	return h, conn, res
}

func ptr[T any](v T) *T { return &v }
