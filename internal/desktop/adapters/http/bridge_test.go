// SPDX-License-Identifier: MIT

package httpadapter_test

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/SDK-E/agentsone/internal/core"
	httpadapter "github.com/SDK-E/agentsone/internal/desktop/adapters/http"
	"github.com/SDK-E/agentsone/internal/desktop/app"
)

// vnc is a fake desktop: each connection is the far end of a pipe, and the
// fake VNC server greets with the RFB banner and echoes what it reads.
type vnc struct {
	mu      sync.Mutex
	humans  []string
	err     error
	servers []net.Conn
	ended   chan struct{} // closed when the first connection's server side ended
}

func (v *vnc) Connect(_ context.Context, human string) (net.Conn, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.humans = append(v.humans, human)
	if v.err != nil {
		return nil, v.err
	}
	client, server := net.Pipe()
	v.servers = append(v.servers, server)
	ended := v.ended
	go func() {
		defer func() {
			_ = server.Close()
			if ended != nil {
				close(ended)
			}
		}()
		if _, err := server.Write([]byte("RFB 003.008\n")); err != nil {
			return
		}
		_, _ = io.Copy(server, server)
	}()
	return client, nil
}

func (v *vnc) connects() []string {
	v.mu.Lock()
	defer v.mu.Unlock()
	return append([]string(nil), v.humans...)
}

var (
	ada   = core.Caller{Kind: core.CallerHuman, Name: "ada", UID: -1}
	agent = core.Caller{Kind: core.CallerAgent, Name: "founder", UID: 0, IsRoot: true}
)

// serve mounts the bridge the way the module does, behind a caller.
func serve(t *testing.T, v *vnc, caller *core.Caller) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.Handle(httpadapter.Pattern, httpadapter.Bridge(v))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if caller != nil {
			r = r.WithContext(core.WithCaller(r.Context(), *caller))
		}
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func wsURL(srv *httptest.Server, human string) string {
	return "ws" + strings.TrimPrefix(srv.URL, "http") + "/desktop/" + human + "/vnc"
}

// answer is the status and headers of a WebSocket handshake; 0 when no
// response came.
type answer struct {
	status int
	header http.Header
}

func dial(t *testing.T, url string, h http.Header, protocols ...string) (*websocket.Conn, answer, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	c, res, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: h, Subprotocols: protocols})
	if res == nil {
		return c, answer{}, err
	}
	if res.Body != nil {
		_ = res.Body.Close()
	}
	return c, answer{status: res.StatusCode, header: res.Header}, err
}

func TestTheHumanReachesTheirDesktopsVNCServer(t *testing.T) {
	v := &vnc{}
	srv := serve(t, v, &ada)
	c, res, err := dial(t, wsURL(srv, "ada"), nil, httpadapter.Subprotocol)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = c.CloseNow() }()
	if res.status != http.StatusSwitchingProtocols || c.Subprotocol() != httpadapter.Subprotocol {
		t.Fatalf("status %d subprotocol %q", res.status, c.Subprotocol())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	typ, msg, err := c.Read(ctx)
	if err != nil || typ != websocket.MessageBinary || string(msg) != "RFB 003.008\n" {
		t.Fatalf("greeting %v %q %v", typ, msg, err)
	}
	if err := c.Write(ctx, websocket.MessageBinary, []byte("RFB 003.008\n")); err != nil {
		t.Fatal(err)
	}
	if _, msg, err := c.Read(ctx); err != nil || string(msg) != "RFB 003.008\n" {
		t.Fatalf("echo %q %v", msg, err)
	}
	if got := v.connects(); len(got) != 1 || got[0] != "ada" {
		t.Errorf("connected desktops %v, want [ada]", got)
	}
}

func TestClosingTheBrowserSideClosesTheVNCConnection(t *testing.T) {
	v := &vnc{ended: make(chan struct{})}
	srv := serve(t, v, &ada)
	c, _, err := dial(t, wsURL(srv, "ada"), nil, httpadapter.Subprotocol)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, _, err := c.Read(ctx); err != nil {
		t.Fatal(err)
	}
	_ = c.Close(websocket.StatusNormalClosure, "bye")
	select {
	case <-v.ended:
	case <-ctx.Done():
		t.Fatal("the VNC connection stayed open after the browser left")
	}
}

func TestTheVNCServerEndingClosesTheWebSocket(t *testing.T) {
	v := &vnc{}
	srv := serve(t, v, &ada)
	c, _, err := dial(t, wsURL(srv, "ada"), nil, httpadapter.Subprotocol)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, _, err := c.Read(ctx); err != nil {
		t.Fatal(err)
	}
	v.mu.Lock()
	_ = v.servers[0].Close()
	v.mu.Unlock()
	if _, _, err := c.Read(ctx); err == nil {
		t.Fatal("read after the VNC server ended succeeded")
	} else if s := websocket.CloseStatus(err); s != websocket.StatusNormalClosure && s != websocket.StatusGoingAway {
		t.Errorf("close status %v (%v), want normal", s, err)
	}
}

func TestRefusals(t *testing.T) {
	other := core.Caller{Kind: core.CallerHuman, Name: "bob", UID: -1}
	anonymous := core.Anonymous
	for _, tc := range []struct {
		name      string
		caller    *core.Caller
		human     string
		origin    string
		protocols []string
		want      int
	}{
		{"nobody signed in", &anonymous, "ada", "", []string{httpadapter.Subprotocol}, http.StatusUnauthorized},
		{"no caller at all", nil, "ada", "", []string{httpadapter.Subprotocol}, http.StatusUnauthorized},
		{"an agent, even root", &agent, "ada", "", []string{httpadapter.Subprotocol}, http.StatusForbidden},
		{"another human's desktop", &other, "ada", "", []string{httpadapter.Subprotocol}, http.StatusForbidden},
		{"cross-site page", &ada, "ada", "https://evil.example", []string{httpadapter.Subprotocol}, http.StatusForbidden},
		{"no subprotocol", &ada, "ada", "", nil, http.StatusBadRequest},
		{"wrong subprotocol", &ada, "ada", "", []string{"binary"}, http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := &vnc{}
			srv := serve(t, v, tc.caller)
			h := http.Header{}
			if tc.origin != "" {
				h.Set("Origin", tc.origin)
			}
			c, res, err := dial(t, wsURL(srv, tc.human), h, tc.protocols...)
			if err == nil {
				_ = c.CloseNow()
				t.Fatal("dial succeeded")
			}
			if res.status != tc.want {
				t.Fatalf("status %d, want %d (%v)", res.status, tc.want, err)
			}
			if got := v.connects(); len(got) != 0 {
				t.Errorf("a refused request reached the desktops: %v", got)
			}
		})
	}
}

func TestAPlainGETIsRefusedWithoutStartingADesktop(t *testing.T) {
	v := &vnc{}
	srv := serve(t, v, &ada)
	res, err := http.Get(srv.URL + "/desktop/ada/vnc")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("status %d, want 400", res.StatusCode)
	}
	if len(v.connects()) != 0 {
		t.Error("a plain GET started a desktop")
	}
}

func TestDesktopErrorsBecomeProblems(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want int
	}{
		{fmt.Errorf("cap: %w", app.ErrTooMany), http.StatusTooManyRequests},
		{fmt.Errorf("labwc died: %w", core.ErrUnavailable), http.StatusServiceUnavailable},
		{fmt.Errorf("no user: %w", core.ErrNotFound), http.StatusNotFound},
	} {
		v := &vnc{err: tc.err}
		srv := serve(t, v, &ada)
		_, res, err := dial(t, wsURL(srv, "ada"), nil, httpadapter.Subprotocol)
		if err == nil || res.status != tc.want {
			t.Errorf("%v: status %d, want %d", tc.err, res.status, tc.want)
			continue
		}
		if ct := res.header.Get("Content-Type"); ct != "application/problem+json" {
			t.Errorf("%v: content type %q", tc.err, ct)
		}
		if tc.want == http.StatusTooManyRequests && res.header.Get("Retry-After") == "" {
			t.Error("429 without Retry-After")
		}
	}
}
