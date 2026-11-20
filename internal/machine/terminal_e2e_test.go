// SPDX-License-Identifier: MIT

package machine_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/SDK-E/agentsone/internal/api"
	apihttp "github.com/SDK-E/agentsone/internal/api/adapters/http"
	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/core/coretest"
	"github.com/SDK-E/agentsone/internal/machine"
	"github.com/SDK-E/agentsone/internal/machine/app"
	"github.com/SDK-E/agentsone/internal/machine/domain"
	"github.com/SDK-E/agentsone/internal/machine/machinetest"
)

// These tests go through the whole TCP chain of the API server (request
// log, security headers, identity, composed modules, the machine module's
// strict middleware, the WebSocket upgrade) with a real listener. Only the
// shell is fake: an echo terminal that records which uid it was started as.

const ptyProtocol = "agentsone.pty.v1"

// echoPty answers every input with the same bytes in upper case.
type echoPty struct {
	in   *io.PipeReader
	inW  *io.PipeWriter
	exit chan int
	once sync.Once
}

func newEchoPty() *echoPty {
	r, w := io.Pipe()
	return &echoPty{in: r, inW: w, exit: make(chan int, 1)}
}

func (p *echoPty) Read(b []byte) (int, error) {
	n, err := p.in.Read(b)
	copy(b, strings.ToUpper(string(b[:n])))
	return n, err
}
func (p *echoPty) Write(b []byte) (int, error) { return p.inW.Write(b) }
func (p *echoPty) Resize(int, int) error       { return nil }
func (p *echoPty) Wait() int                   { return <-p.exit }
func (p *echoPty) Close() error {
	p.once.Do(func() { _ = p.inW.Close(); p.exit <- 129 })
	return nil
}

// uidStarter records the uid of every terminal it starts.
type uidStarter struct {
	mu   sync.Mutex
	uids []int
}

func (s *uidStarter) Start(_ context.Context, _ domain.PtySpec, uid, _ int, _ string, _ core.ProcessCell) (app.PtyProcess, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.uids = append(s.uids, uid)
	return newEchoPty(), nil
}

func (s *uidStarter) started() []int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]int(nil), s.uids...)
}

type accounts struct{}

func (accounts) List(context.Context) ([]domain.MachineUser, error) {
	return []domain.MachineUser{
		{Name: "root", UID: 0, GID: 0, Shell: "/bin/bash", Home: "/root"},
		{Name: "writer", UID: 200001, GID: 200001, Shell: "/usr/sbin/nologin", Home: "/home/agents/writer"},
		{Name: "editor", UID: 200002, GID: 200002, Shell: "/usr/sbin/nologin", Home: "/home/agents/editor"},
	}, nil
}

// door is the daemon's TCP listener with the machine module behind it.
type door struct {
	url     string // http://127.0.0.1:port
	module  *machine.Module
	starter *uidStarter
	human   string // session token of a signed-in human
	writer  string // bearer token of the custom agent "writer"
	editor  string // bearer token of the custom agent "editor"
}

func newDoor(t *testing.T) *door {
	t.Helper()
	deps, fakes := coretest.NewDeps(t)
	sys := machinetest.NoopSystem()
	d := &door{starter: &uidStarter{}}
	sys.Pty, sys.Users = d.starter, accounts{}
	d.module = machine.NewWith(deps, sys)
	srv, err := api.NewServer([]core.Module{d.module}, deps)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.TCPHandler())
	t.Cleanup(ts.Close)
	d.url = ts.URL
	issue := func(c core.Caller) string {
		tok, err := fakes.Tokens.Issue(context.Background(), c, nil, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		return tok.Value
	}
	d.human = issue(core.Caller{Kind: core.CallerHuman, Name: "alice", UID: -1})
	d.writer = issue(core.Caller{Kind: core.CallerAgent, Name: "writer", UID: 200001, Assurance: core.AssuranceSoft})
	d.editor = issue(core.Caller{Kind: core.CallerAgent, Name: "editor", UID: 200002, Assurance: core.AssuranceSoft})
	return d
}

// asHuman is what the browser sends: the session cookie and its own origin.
func (d *door) asHuman(h http.Header) {
	h.Set("Cookie", apihttp.CookieName+"="+d.human)
	h.Set("Origin", d.url)
}

func asAgent(token string) func(http.Header) {
	return func(h http.Header) { h.Set("Authorization", "Bearer "+token) }
}

func (d *door) call(t *testing.T, who func(http.Header), method, path, body string) (int, http.Header, []byte) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), method, d.url+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if who != nil {
		who(req.Header)
	}
	resp, err := (&http.Client{Transport: &http.Transport{DisableKeepAlives: true}}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }() // test response body
	out, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, resp.Header, out
}

func (d *door) open(t *testing.T, who func(http.Header)) string {
	t.Helper()
	status, _, body := d.call(t, who, http.MethodPost, "/v1/machine/pty", `{}`)
	if status != http.StatusCreated {
		t.Fatalf("open terminal: %d %s", status, body)
	}
	var s struct {
		ID   string `json:"id"`
		User string `json:"user"`
	}
	if err := json.Unmarshal(body, &s); err != nil {
		t.Fatal(err)
	}
	return s.ID
}

// dial opens the terminal's WebSocket and reports the HTTP status of a
// refusal.
func (d *door) dial(id string, who func(http.Header), protocols ...string) (*websocket.Conn, int, error) {
	h := http.Header{}
	if who != nil {
		who(h)
	}
	url := "ws" + strings.TrimPrefix(d.url, "http") + "/v1/machine/pty/" + id + "/socket"
	ws, resp, err := websocket.Dial(context.Background(), url, &websocket.DialOptions{Subprotocols: protocols, HTTPHeader: h})
	status := 0
	if resp != nil {
		status = resp.StatusCode
		if resp.Body != nil {
			_ = resp.Body.Close() // test response body
		}
	}
	return ws, status, err
}

func TestTerminalRoundTripForASignedInHuman(t *testing.T) {
	d := newDoor(t)
	id := d.open(t, d.asHuman)

	ws, status, err := d.dial(id, d.asHuman, ptyProtocol)
	if err != nil {
		t.Fatalf("connect: %d %v", status, err)
	}
	defer func() { _ = ws.CloseNow() }()
	if ws.Subprotocol() != ptyProtocol {
		t.Fatalf("subprotocol %q", ws.Subprotocol())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := ws.Write(ctx, websocket.MessageBinary, []byte("ls\n")); err != nil {
		t.Fatal(err)
	}
	typ, out, err := ws.Read(ctx)
	if err != nil || typ != websocket.MessageBinary || string(out) != "LS\n" {
		t.Fatalf("output %v %q %v", typ, out, err)
	}
	if got := d.starter.started(); len(got) != 1 || got[0] != 0 {
		t.Fatalf("a human's terminal started as uids %v, want root", got)
	}
}

func TestTerminalSocketRefusesWhatIsNotTheSignedInHumansOwnBrowser(t *testing.T) {
	d := newDoor(t)
	id := d.open(t, d.asHuman)

	cases := map[string]struct {
		who    func(http.Header)
		protos []string
		want   int
	}{
		"no credentials": {who: func(h http.Header) { h.Set("Origin", d.url) }, protos: []string{ptyProtocol}, want: http.StatusUnauthorized},
		"a cross-site page with the session cookie": {
			who: func(h http.Header) {
				d.asHuman(h)
				h.Set("Origin", "https://evil.example")
			},
			protos: []string{ptyProtocol}, want: http.StatusForbidden,
		},
		"no subprotocol": {who: d.asHuman, want: http.StatusBadRequest},
	}
	for name, tc := range cases {
		ws, status, err := d.dial(id, tc.who, tc.protos...)
		if err == nil {
			_ = ws.CloseNow()
			t.Fatalf("%s: connected", name)
		}
		if status != tc.want {
			t.Errorf("%s: status %d (%v), want %d", name, status, err, tc.want)
		}
	}

	// The refusals must not have taken the terminal's one client slot.
	ws, status, err := d.dial(id, d.asHuman, ptyProtocol)
	if err != nil {
		t.Fatalf("connect after the refusals: %d %v", status, err)
	}
	_ = ws.CloseNow()
}

func TestTerminalHasOneClient(t *testing.T) {
	d := newDoor(t)
	id := d.open(t, d.asHuman)
	first, _, err := d.dial(id, d.asHuman, ptyProtocol)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = first.CloseNow() }()
	second, status, err := d.dial(id, d.asHuman, ptyProtocol)
	if err == nil {
		_ = second.CloseNow()
		t.Fatal("a second client connected")
	}
	if status != http.StatusConflict {
		t.Fatalf("second client: %d %v, want 409", status, err)
	}
}

// The origin is checked before the client slot is looked at: a cross-site
// page learns nothing about the terminal, not even that it has a client.
func TestCrossSiteAttemptIsRefusedBeforeTheClientSlotIsLookedAt(t *testing.T) {
	d := newDoor(t)
	id := d.open(t, d.asHuman)
	first, _, err := d.dial(id, d.asHuman, ptyProtocol)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = first.CloseNow() }()
	crossSite := func(h http.Header) {
		d.asHuman(h)
		h.Set("Origin", "https://evil.example")
	}
	ws, status, err := d.dial(id, crossSite, ptyProtocol)
	if err == nil {
		_ = ws.CloseNow()
		t.Fatal("a cross-site page connected")
	}
	if status != http.StatusForbidden {
		t.Fatalf("cross-site attempt on a busy terminal: %d, want 403 (409 would show the slot is taken)", status)
	}
}

func TestAgentTerminalRunsAsItsOwnUserAndSeesOnlyItsOwn(t *testing.T) {
	d := newDoor(t)
	humans := d.open(t, d.asHuman)
	writers := d.open(t, asAgent(d.writer))
	if got := d.starter.started(); len(got) != 2 || got[0] != 0 || got[1] != 200001 {
		t.Fatalf("started as uids %v, want root then the agent's own 200001", got)
	}

	status, _, body := d.call(t, asAgent(d.writer), http.MethodGet, "/v1/machine/pty", "")
	if status != http.StatusOK {
		t.Fatalf("list: %d %s", status, body)
	}
	var list struct {
		Items []struct {
			ID   string `json:"id"`
			User string `json:"user"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 1 || list.Items[0].ID != writers || list.Items[0].User != "writer" {
		t.Fatalf("the agent sees %+v, want only its own terminal", list.Items)
	}

	// The human's terminal and a peer's terminal are not there for the agent.
	for _, other := range []struct{ owner, id string }{{"the human", humans}, {"the editor", d.open(t, asAgent(d.editor))}} {
		if status, _, _ := d.call(t, asAgent(d.writer), http.MethodGet, "/v1/machine/pty/"+other.id, ""); status != http.StatusNotFound {
			t.Errorf("get %s's terminal: %d, want 404", other.owner, status)
		}
		if status, _, _ := d.call(t, asAgent(d.writer), http.MethodDelete, "/v1/machine/pty/"+other.id, ""); status != http.StatusNotFound {
			t.Errorf("close %s's terminal: %d, want 404", other.owner, status)
		}
		ws, status, err := d.dial(other.id, asAgent(d.writer), ptyProtocol)
		if err == nil {
			_ = ws.CloseNow()
			t.Fatalf("the agent attached to %s's terminal", other.owner)
		}
		if status != http.StatusNotFound {
			t.Errorf("attach to %s's terminal: %d, want 404", other.owner, status)
		}
	}

	// Its own terminal works over the socket.
	ws, status, err := d.dial(writers, asAgent(d.writer), ptyProtocol)
	if err != nil {
		t.Fatalf("own terminal: %d %v", status, err)
	}
	defer func() { _ = ws.CloseNow() }()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := ws.Write(ctx, websocket.MessageBinary, []byte("id\n")); err != nil {
		t.Fatal(err)
	}
	if _, out, err := ws.Read(ctx); err != nil || string(out) != "ID\n" {
		t.Fatalf("output %q %v", out, err)
	}
}

func TestTerminalCapAnswers429WithRetryAfterThroughTheDoor(t *testing.T) {
	d := newDoor(t)
	for range domain.MaxPtyPerActor {
		d.open(t, asAgent(d.writer))
	}
	status, header, body := d.call(t, asAgent(d.writer), http.MethodPost, "/v1/machine/pty", `{}`)
	if status != http.StatusTooManyRequests || header.Get("Retry-After") == "" {
		t.Fatalf("over the cap: %d retry-after %q %s", status, header.Get("Retry-After"), body)
	}
	// Another caller is not held back by the writer's cap.
	d.open(t, asAgent(d.editor))
}

func TestStoppingTheModuleHangsUpOpenTerminalsAndTheirSockets(t *testing.T) {
	d := newDoor(t)
	id := d.open(t, d.asHuman)
	ws, _, err := d.dial(id, d.asHuman, ptyProtocol)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ws.CloseNow() }()

	if err := d.module.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		if _, _, err := ws.Read(ctx); err != nil {
			if ctx.Err() != nil {
				t.Fatal("the socket stayed open after the module stopped")
			}
			break
		}
	}
	if status, _, _ := d.call(t, d.asHuman, http.MethodGet, "/v1/machine/pty/"+id, ""); status != http.StatusNotFound {
		t.Fatalf("the terminal is still listed after stop: %d", status)
	}
}
