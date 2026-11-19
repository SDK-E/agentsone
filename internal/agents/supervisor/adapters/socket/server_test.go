// SPDX-License-Identifier: MIT

package socket_test

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/agents/supervisor/adapters/socket"
	"github.com/SDK-E/agentsone/internal/agents/supervisor/app"
	"github.com/SDK-E/agentsone/internal/agents/supervisor/domain"
	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/core/coretest"
)

const waitFor = 5 * time.Second

// peerRunner is a protocol test peer: a minimal runner built only from the
// protocol, used until the echo runner binary exists.
type peerRunner struct {
	t     *testing.T
	nc    net.Conn
	sc    *bufio.Scanner
	ended chan struct{}
}

func dialRunner(t *testing.T, path string) *peerRunner {
	t.Helper()
	nc, err := net.Dial("unix", path)
	if err != nil {
		t.Fatalf("runner dial: %v", err)
	}
	t.Cleanup(func() { _ = nc.Close() })
	return &peerRunner{t: t, nc: nc, sc: bufio.NewScanner(nc), ended: make(chan struct{})}
}

func (p *peerRunner) send(line string) {
	p.t.Helper()
	if _, err := p.nc.Write([]byte(line + "\n")); err != nil {
		p.t.Fatal(err)
	}
}

func (p *peerRunner) read() map[string]any {
	p.t.Helper()
	got := make(chan map[string]any, 1)
	go func() {
		if !p.sc.Scan() {
			got <- nil
			return
		}
		var m map[string]any
		_ = json.Unmarshal(p.sc.Bytes(), &m)
		got <- m
	}()
	select {
	case m := <-got:
		if m == nil {
			p.t.Fatal("daemon closed the socket")
		}
		return m
	case <-time.After(waitFor):
		p.t.Fatal("nothing from the daemon within 5s")
		return nil
	}
}

type startLauncher struct {
	started chan string
}

func (l *startLauncher) Install(context.Context, domain.UnitSpec) error { return nil }
func (l *startLauncher) Start(_ context.Context, a string) error        { l.started <- a; return nil }
func (l *startLauncher) Restart(_ context.Context, a string) error      { l.started <- a; return nil }
func (l *startLauncher) Stop(context.Context, string) error             { return nil }
func (l *startLauncher) Remove(context.Context, string) error           { return nil }
func (l *startLauncher) Mode(string) domain.LimitMode                   { return domain.LimitsNone }

type collect struct {
	mu     sync.Mutex
	chunks []app.OutputChunk
	sums   map[string]app.Summary
}

func (c *collect) Output(_ context.Context, _ string, o app.OutputChunk) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.chunks = append(c.chunks, o)
	return nil
}
func (c *collect) RecordUsage(context.Context, string, app.Usage) error { return nil }
func (c *collect) Load(_ context.Context, a string) (app.Summary, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sums[a], nil
}
func (c *collect) Save(_ context.Context, a string, s app.Summary) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sums[a] = s
	return nil
}
func (c *collect) GatewayToken(context.Context, string) (string, error) { return "test-gw", nil }
func (c *collect) Identity(_ context.Context, a string) (app.AgentIdentity, error) {
	return app.AgentIdentity{Agent: core.Agent{Name: a, Kind: core.AgentAlwaysOn, IsRoot: true}, Team: a, Charter: "c", OneLine: "Goal: g. Now: n.", Goals: []string{"g"}}, nil
}

// One agent's whole life over a real socket: wake starts the runner, the
// handshake (H3, H7, H8), a chat turn (T1-T6, P1), then sleep (S1) and shutdown (X1).
func TestServer_RunsAgentLifecycleOverRealSocket(t *testing.T) {
	t.Parallel()
	dir, err := os.MkdirTemp("", "ao") // B5c: short socket path
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	fakes, ports := coretest.NewFakes()
	c := &collect{sums: map[string]app.Summary{}}
	inner := &startLauncher{started: make(chan string, 4)}
	srv := &socket.Server{Dir: dir, HelloDeadline: func() <-chan struct{} { return make(chan struct{}) }}
	var chowned int
	srv.Chown = func(_ string, uid int) error { chowned = uid; return nil }
	launcher := &socket.Launcher{Inner: inner, Server: srv, Ctx: t.Context()}
	t.Cleanup(func() { _ = srv.Shutdown() })
	sup := app.New(app.Deps{Clock: fakes.Clock, IDs: fakes.IDs, Ports: ports, Launcher: launcher, Identity: c, Output: c, Summaries: c, Usage: c, Gateway: c, Config: app.DefaultConfig()})
	srv.Supervisor = sup
	if err := launcher.Install(t.Context(), domain.UnitSpec{Agent: "founder", Account: domain.AccountFor("founder", true)}); err != nil {
		t.Fatal(err)
	}

	if err := sup.Wake(t.Context(), "founder", core.WakeChat, json.RawMessage(`{"id":"msg_01JD8X3Q5Y6Z7A8B9C0D1E2F41","chatId":"cht_01JD8X3Q5Y6Z7A8B9C0D1E2F40","from":"human:alice","text":"Hello founder","sentAt":"2026-11-19T19:00:01Z"}`)); err != nil {
		t.Fatal(err)
	}
	if got := <-inner.started; got != "founder" {
		t.Fatalf("started %q", got)
	}
	info, err := os.Stat(srv.Path("founder"))
	if err != nil || info.Mode().Perm() != 0o600 || chowned != 0 {
		t.Fatalf("socket mode = %v, owner uid = %d, err = %v; want 0600 owned by root", info.Mode().Perm(), chowned, err)
	}

	r := dialRunner(t, srv.Path("founder"))
	r.send(`{"jsonrpc":"2.0","id":1,"method":"hello","params":{"protocolVersions":["1.0"],"runner":{"name":"peer","version":"1.0.0"},"capabilities":{"streaming":true,"cancel":true,"compaction":true,"resume":true}}}`)
	if w := r.read(); w["id"] != float64(1) || w["result"] == nil {
		t.Fatalf("H3: first line = %v, want the welcome", w)
	}
	r.send(`{"jsonrpc":"2.0","method":"status","params":{"state":"idle"}}`)
	wake := r.read()
	params := wake["params"].(map[string]any)
	turn := params["turnId"].(string)
	if wake["method"] != "wake" || wake["id"] != float64(1) || params["reason"] != "chat" {
		t.Fatalf("held wake = %v", wake)
	}
	r.send(`{"jsonrpc":"2.0","id":1,"result":{}}`)
	r.send(`{"jsonrpc":"2.0","method":"status","params":{"state":"working","turnId":"` + turn + `"}}`)
	r.send(`{"jsonrpc":"2.0","method":"profile.update","params":{"turnId":"` + turn + `","line":"Goal: keep this machine useful. Now: answering alice."}}`)
	r.send(`{"jsonrpc":"2.0","method":"output","params":{"turnId":"` + turn + `","chatId":"cht_01JD8X3Q5Y6Z7A8B9C0D1E2F40","chunk":0,"text":"Hello founder","final":true}}`)
	r.send(`{"jsonrpc":"2.0","method":"status","params":{"state":"idle","turnId":"` + turn + `"}}`)

	sleepDone := make(chan error, 1)
	go func() { sleepDone <- sup.Sleep(context.Background(), "founder", true) }()
	sleep := r.read()
	if sleep["method"] != "sleep" || sleep["id"] != float64(2) {
		t.Fatalf("sleep request = %v", sleep)
	}
	r.send(`{"jsonrpc":"2.0","id":2,"result":{"summary":"Chatted with alice."}}`)
	r.send(`{"jsonrpc":"2.0","method":"status","params":{"state":"sleeping"}}`)
	shutdown := r.read()
	if shutdown["method"] != "shutdown" || shutdown["id"] != float64(3) {
		t.Fatalf("shutdown request = %v", shutdown)
	}
	r.send(`{"jsonrpc":"2.0","id":3,"result":{}}`)
	select {
	case err := <-sleepDone:
		if err != nil {
			t.Fatalf("Sleep() error = %v", err)
		}
	case <-time.After(waitFor):
		t.Fatal("Sleep did not return within 5s")
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.chunks) != 1 || c.chunks[0].Text != "Hello founder" || !c.chunks[0].Final {
		t.Errorf("output = %+v", c.chunks)
	}
	if c.sums["founder"].Text != "Chatted with alice." {
		t.Errorf("summary = %+v", c.sums["founder"])
	}
	lines, _ := fakes.Live.Lines(t.Context())
	if len(lines) != 1 || !strings.Contains(lines[0].Line, "answering alice") {
		t.Errorf("live lines = %+v", lines)
	}
	for _, e := range fakes.Audit.Entries() {
		if e.Action == app.ActionTurnFault {
			t.Errorf("turn with profile.update recorded as fault: %+v", e)
		}
	}
}

func TestServer_ListenIsIdempotentAndCloseRemovesSocket(t *testing.T) {
	t.Parallel()
	dir, err := os.MkdirTemp("", "ao")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	srv := &socket.Server{Dir: dir, Chown: func(string, int) error { return nil }}
	if err := srv.Listen(t.Context(), "mira", 1001); err != nil {
		t.Fatal(err)
	}
	if err := srv.Listen(t.Context(), "mira", 1001); err != nil {
		t.Fatalf("second Listen error = %v", err)
	}
	if err := srv.Listen(t.Context(), "../x", 0); err == nil {
		t.Fatal("Listen accepted a bad agent name")
	}
	if err := srv.Close("mira"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(srv.Path("mira")); !os.IsNotExist(err) {
		t.Fatalf("socket still exists: %v", err)
	}
	if err := srv.Close("mira"); err != nil {
		t.Fatalf("closing a closed socket error = %v", err)
	}
	if err := srv.Shutdown(); err != nil {
		t.Fatal(err)
	}
}
