// SPDX-License-Identifier: MIT

package httpadapter_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/SDK-E/agentsone/internal/contract/api/machineapi"
	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/core/coretest"
	httpadapter "github.com/SDK-E/agentsone/internal/machine/adapters/http"
	"github.com/SDK-E/agentsone/internal/machine/app"
	"github.com/SDK-E/agentsone/internal/machine/domain"
)

// echoPty echoes input as output, upper-cased, until it is told to exit.
type echoPty struct {
	in   *io.PipeReader
	inW  *io.PipeWriter
	mu   sync.Mutex
	size [2]int
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
func (p *echoPty) Write(b []byte) (int, error) {
	if string(b) == "exit\n" {
		p.exit <- 4
		_ = p.inW.Close()
		return len(b), nil
	}
	return p.inW.Write(b)
}
func (p *echoPty) Resize(cols, rows int) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.size = [2]int{cols, rows}
	return nil
}
func (p *echoPty) Wait() int { return <-p.exit }
func (p *echoPty) Close() error {
	p.once.Do(func() { _ = p.inW.Close(); p.exit <- 129 })
	return nil
}

type echoStarter struct {
	mu   sync.Mutex
	last *echoPty
}

func (s *echoStarter) Start(context.Context, domain.PtySpec, int, int, string) (app.PtyProcess, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.last = newEchoPty()
	return s.last, nil
}

type ptyUsers struct{}

func (ptyUsers) List(context.Context) ([]domain.MachineUser, error) {
	return []domain.MachineUser{
		{Name: "root", UID: 0, Shell: "/bin/bash", Home: "/root"},
		{Name: "writer", UID: 200001, GID: 200001, Shell: "/usr/sbin/nologin", Home: "/home/agents/writer"},
	}, nil
}

func servePty(t *testing.T) (http.Handler, *echoStarter) {
	t.Helper()
	h, st, _, _ := servePtyParts(t)
	return h, st
}

// servePtyParts also returns the terminal table and its clock, for tests
// that age terminals.
func servePtyParts(t *testing.T) (http.Handler, *echoStarter, *app.Terminals, *coretest.Clock) {
	t.Helper()
	st := &echoStarter{}
	clock := coretest.NewClock(coretest.Epoch)
	terms := app.NewTerminals(st, ptyUsers{}, clock, coretest.NewIDs())
	m := app.New(app.Deps{Audit: coretest.NewAudit(), Clock: coretest.NewClock(coretest.Epoch), IDs: coretest.NewIDs()})
	handler := httpadapter.New(m, func(name string) string { return "/home/agents/" + name }).WithTerminals(terms)
	mux := http.NewServeMux()
	machineapi.Group(handler, handler.Terminal).Register(mux, core.APIOptions{
		RequestError: func(w http.ResponseWriter, r *http.Request, err error) {
			core.WriteProblem(w, r, core.NewProblem(http.StatusBadRequest, "validation", err.Error()))
		},
		ResponseError: core.WriteError,
	})
	return mux, st, terms, clock
}

// A refused cross-site attempt must leave the terminal as it was, idle
// timer included: it never attached, so it cannot restart the 30 minutes.
func TestCrossSiteAttemptDoesNotRestartTheIdleTimer(t *testing.T) {
	h, _, terms, clock := servePtyParts(t)
	id := decode[map[string]any](t, call(t, h, &asHuman, http.MethodPost, "/v1/machine/pty", `{}`))["id"].(string)
	srv := httptest.NewServer(withCaller(asHuman, h))
	defer srv.Close()
	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/v1/machine/pty/" + id + "/socket"

	clock.Advance(domain.PtyIdleTimeout - time.Minute)
	ws, resp, err := websocket.Dial(context.Background(), url, &websocket.DialOptions{
		Subprotocols: []string{"agentsone.pty.v1"},
		HTTPHeader:   http.Header{"Origin": {"https://evil.example"}},
	})
	if err == nil {
		_ = ws.CloseNow()
		t.Fatal("cross-site attempt connected")
	}
	if resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-site attempt: %v %v", resp, err)
	}
	_ = resp.Body.Close() // test response body
	clock.Advance(2 * time.Minute)
	terms.CloseIdle(context.Background())
	if _, err := terms.Get(context.Background(), domain.Actor{Kind: domain.ActorHuman, Name: "alice", UID: -1}, id); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("terminal idle for %s is still there: %v", domain.PtyIdleTimeout+time.Minute, err)
	}
}

func TestTerminalRest(t *testing.T) {
	h, _ := servePty(t)
	rec := call(t, h, &asCustom, http.MethodPost, "/v1/machine/pty", `{"cols":100,"rows":30}`)
	want(t, rec, http.StatusCreated)
	s := decode[map[string]any](t, rec)
	id, _ := s["id"].(string)
	if s["user"] != "writer" || s["socket_path"] != "/v1/machine/pty/"+id+"/socket" || s["exited"] != false {
		t.Fatalf("session %v", s)
	}
	want(t, call(t, h, &asCustom, http.MethodGet, "/v1/machine/pty/"+id, ""), http.StatusOK)
	want(t, call(t, h, &asCustom, http.MethodGet, "/v1/machine/pty?limit=5", ""), http.StatusOK)
	want(t, call(t, h, &asHuman, http.MethodPost, "/v1/machine/pty", `{"cols":3}`), http.StatusBadRequest)
	want(t, call(t, h, &asCustom, http.MethodDelete, "/v1/machine/pty/"+id, ""), http.StatusNoContent)
	want(t, call(t, h, &asCustom, http.MethodGet, "/v1/machine/pty/"+id, ""), http.StatusNotFound)
}

func TestTerminalCapAnswers429(t *testing.T) {
	h, _ := servePty(t)
	for range domain.MaxPtyPerActor {
		want(t, call(t, h, &asCustom, http.MethodPost, "/v1/machine/pty", `{}`), http.StatusCreated)
	}
	rec := call(t, h, &asCustom, http.MethodPost, "/v1/machine/pty", `{}`)
	want(t, rec, http.StatusTooManyRequests)
	if rec.Header().Get("Retry-After") == "" {
		t.Fatal("no Retry-After")
	}
}

func withCaller(c core.Caller, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.ServeHTTP(w, r.WithContext(core.WithCaller(r.Context(), c)))
	})
}

func TestTerminalSocket(t *testing.T) {
	h, st := servePty(t)
	rec := call(t, h, &asHuman, http.MethodPost, "/v1/machine/pty", `{}`)
	id := decode[map[string]any](t, rec)["id"].(string)
	srv := httptest.NewServer(withCaller(asHuman, h))
	defer srv.Close()
	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/v1/machine/pty/" + id + "/socket"
	c := context.Background()

	if _, status, err := dial(c, url); err == nil || status != http.StatusBadRequest {
		t.Fatalf("without the subprotocol: %d %v", status, err)
	}
	ws, _, err := dial(c, url, "agentsone.pty.v1")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ws.CloseNow() }()
	if ws.Subprotocol() != "agentsone.pty.v1" {
		t.Fatalf("subprotocol %q", ws.Subprotocol())
	}
	if second, status, err := dial(c, url, "agentsone.pty.v1"); err == nil || status != http.StatusConflict {
		if second != nil {
			_ = second.CloseNow()
		}
		t.Fatalf("second client: %d %v", status, err)
	}
	if err := ws.Write(c, websocket.MessageText, []byte(`{"type":"resize","cols":90,"rows":20}`)); err != nil {
		t.Fatal(err)
	}
	if err := ws.Write(c, websocket.MessageBinary, []byte("hi\n")); err != nil {
		t.Fatal(err)
	}
	typ, out, err := ws.Read(c)
	if err != nil || typ != websocket.MessageBinary || string(out) != "HI\n" {
		t.Fatalf("output %v %q %v", typ, out, err)
	}
	st.mu.Lock()
	size := st.last.size
	st.mu.Unlock()
	if size != [2]int{90, 20} {
		t.Fatalf("size %v", size)
	}
	if err := ws.Write(c, websocket.MessageBinary, []byte("exit\n")); err != nil {
		t.Fatal(err)
	}
	for {
		typ, msg, err := ws.Read(c)
		if err != nil {
			t.Fatalf("no exit message: %v", err)
		}
		if typ == websocket.MessageText {
			var m map[string]any
			if err := json.Unmarshal(msg, &m); err != nil || m["type"] != "exit" || m["code"] != 4.0 {
				t.Fatalf("exit message %s", msg)
			}
			break
		}
	}
}

func TestTerminalSocketUnknownSession(t *testing.T) {
	h, _ := servePty(t)
	srv := httptest.NewServer(withCaller(asHuman, h))
	defer srv.Close()
	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/v1/machine/pty/pty_01JD7ZK3M00000000000000001/socket"
	if _, status, err := dial(context.Background(), url, "agentsone.pty.v1"); err == nil || status != http.StatusNotFound {
		t.Fatalf("unknown session: %d %v", status, err)
	}
}

// dial opens a WebSocket and returns the HTTP status of a refusal.
func dial(c context.Context, url string, protocols ...string) (*websocket.Conn, int, error) {
	ws, resp, err := websocket.Dial(c, url, &websocket.DialOptions{Subprotocols: protocols})
	status := 0
	if resp != nil {
		status = resp.StatusCode
		if resp.Body != nil {
			_ = resp.Body.Close()
		}
	}
	return ws, status, err
}
