// SPDX-License-Identifier: MIT

package proxy_test

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/edge/door/adapters/proxy"
)

// door serves the proxy in front of upstream on a real loopback listener.
func door(t *testing.T, upstream http.Handler) (doorURL string) {
	t.Helper()
	up := httptest.NewServer(upstream)
	t.Cleanup(up.Close)
	u, err := url.Parse(up.URL)
	if err != nil {
		t.Fatal(err)
	}
	d := httptest.NewServer(proxy.New(u, slog.New(slog.DiscardHandler)))
	t.Cleanup(d.Close)
	return d.URL
}

func do(t *testing.T, req *http.Request) *http.Response {
	t.Helper()
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	t.Cleanup(func() { _ = res.Body.Close() })
	return res
}

func TestProxy_ForwardsRequestsToTheMachine(t *testing.T) {
	t.Parallel()
	u := door(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Seen", r.Method+" "+r.URL.RequestURI())
	}))
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, u+"/v1/agents?limit=2", nil)
	res := do(t, req)
	defer func() { _ = res.Body.Close() }()
	if got := res.Header.Get("X-Seen"); res.StatusCode != http.StatusOK || got != "GET /v1/agents?limit=2" {
		t.Fatalf("got %d %q", res.StatusCode, got)
	}
}

func TestProxy_ReplacesForwardingAndIdentityHeaders(t *testing.T) {
	t.Parallel()
	seen := make(chan http.Header, 1)
	u := door(t, http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { seen <- r.Header.Clone() }))
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, u+"/", nil)
	for k, v := range map[string]string{
		"X-Forwarded-For": "6.6.6.6", "X-Forwarded-Proto": "https", "X-Forwarded-Host": "evil",
		"Forwarded": "for=6.6.6.6", "X-Real-IP": "6.6.6.6", "X-Remote-User": "root",
		"Remote-User": "root", "X-Auth-Request-User": "root", "X-Forwarded-User": "root",
		"Cookie": "session=abc",
	} {
		req.Header.Set(k, v)
	}
	res := do(t, req)
	defer func() { _ = res.Body.Close() }()
	got := <-seen
	if v := got.Values("X-Forwarded-For"); len(v) != 1 || v[0] != "127.0.0.1" {
		t.Errorf("X-Forwarded-For = %q, want only the client address 127.0.0.1", v)
	}
	if v := got.Get("X-Forwarded-Proto"); v != "https" {
		t.Errorf("X-Forwarded-Proto = %q, want https (upgrade claim kept)", v)
	}
	for _, name := range []string{
		"X-Forwarded-Host", "Forwarded", "X-Real-Ip", "X-Remote-User", "Remote-User",
		"X-Auth-Request-User", "X-Forwarded-User",
	} {
		if v := got.Values(name); len(v) != 0 {
			t.Errorf("%s = %q reached the machine", name, v)
		}
	}
	if got.Get("Cookie") != "session=abc" {
		t.Errorf("Cookie = %q, want the daemon session passed on", got.Get("Cookie"))
	}
}

func TestProxy_SaysHTTPWithoutAClaim(t *testing.T) {
	t.Parallel()
	seen := make(chan string, 1)
	u := door(t, http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { seen <- r.Header.Get("X-Forwarded-Proto") }))
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, u+"/", nil)
	res := do(t, req)
	defer func() { _ = res.Body.Close() }()
	if got := <-seen; got != "http" {
		t.Fatalf("X-Forwarded-Proto = %q, want http", got)
	}
}

func TestProxy_StreamsServerSentEventsWithoutBuffering(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	u := door(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: first\n\n")
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, u+"/v1/events/stream", nil)
	res := do(t, req)
	defer func() { _ = res.Body.Close() }()
	line, err := bufio.NewReader(res.Body).ReadString('\n')
	if err != nil || line != "data: first\n" {
		t.Fatalf("first event = %q, %v; want it before the stream ends", line, err)
	}
}

func TestProxy_UpgradesWebSockets(t *testing.T) {
	t.Parallel()
	u := door(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Upgrade") != "websocket" {
			http.Error(w, "want upgrade", http.StatusBadRequest)
			return
		}
		c, rw, err := http.NewResponseController(w).Hijack()
		if err != nil {
			return
		}
		defer func() { _ = c.Close() }()
		_, _ = rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n")
		_ = rw.Flush()
		buf := make([]byte, 4)
		if _, err := io.ReadFull(rw, buf); err == nil {
			_, _ = rw.Write(buf)
			_ = rw.Flush()
		}
	}))
	host := strings.TrimPrefix(u, "http://")
	c, err := net.Dial("tcp", host)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	if err := c.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	_, _ = io.WriteString(c, "GET /v1/machine/pty HTTP/1.1\r\nHost: "+host+
		"\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: "+
		base64.StdEncoding.EncodeToString([]byte("door test nonce."))+"\r\nSec-WebSocket-Version: 13\r\n\r\n")
	br := bufio.NewReader(c)
	res, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatalf("read upgrade answer: %v", err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("status = %d, want 101", res.StatusCode)
	}
	if _, err := io.WriteString(c, "ping"); err != nil {
		t.Fatal(err)
	}
	echo := make([]byte, 4)
	if _, err := io.ReadFull(br, echo); err != nil || string(echo) != "ping" {
		t.Fatalf("echo = %q, %v; want ping through the tunnel", echo, err)
	}
}

func TestProxy_AnswersAProblemWhenTheMachineIsDown(t *testing.T) {
	t.Parallel()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	down := &url.URL{Scheme: "http", Host: ln.Addr().String()}
	_ = ln.Close()
	d := httptest.NewServer(proxy.New(down, slog.New(slog.DiscardHandler)))
	t.Cleanup(d.Close)
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, d.URL+"/", nil)
	res := do(t, req)
	defer func() { _ = res.Body.Close() }()
	var p struct {
		Type string `json:"type"`
	}
	_ = json.NewDecoder(res.Body).Decode(&p)
	if res.StatusCode != http.StatusBadGateway || p.Type != "urn:agentsone:problem:unavailable" ||
		res.Header.Get("Content-Type") != "application/problem+json" {
		t.Fatalf("got %d %q %+v, want 502 unavailable problem", res.StatusCode, res.Header.Get("Content-Type"), p)
	}
}
