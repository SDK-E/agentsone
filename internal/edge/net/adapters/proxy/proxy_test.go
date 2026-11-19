// SPDX-License-Identifier: MIT

package proxy_test

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/edge/net/adapters/proxy"
	"github.com/SDK-E/agentsone/internal/edge/net/app"
	"github.com/SDK-E/agentsone/internal/edge/net/domain"
	"github.com/SDK-E/agentsone/internal/edge/net/nettest"
)

const (
	allowedName = "allowed.example.com"
	echoName    = "echo.example.com"
	blockedName = "blocked.example.com"
	allowedIP   = "203.0.113.10"
	echoIP      = "203.0.113.11"
)

// fixture is the three proxies in front of a fake internet: allowed.example.com is an HTTP and
// an HTTPS test server, echo.example.com echoes bytes on port 443, and nothing else exists.
type fixture struct {
	filter   *app.Filter
	observer *nettest.Observer
	dialer   *fakeDialer
	httpAddr string
	tlsAddr  string
	proxy    string
	seen     chan *http.Request
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{observer: nettest.NewObserver(), seen: make(chan *http.Request, 16)}
	resolver := nettest.NewResolver()
	resolver.Set(allowedName, allowedIP)
	resolver.Set(echoName, echoIP)
	resolver.Set(blockedName, "203.0.113.20")
	var entries []domain.Entry
	for _, n := range []string{allowedName, echoName} {
		p, _ := domain.ParsePattern(n)
		entries = append(entries, domain.Entry{Pattern: p})
	}
	list, _ := domain.NewAllowList(entries)
	f.filter = app.NewFilter(app.FilterConfig{
		Clock:       nettest.NewClock(time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)),
		Resolver:    resolver,
		Observer:    f.observer,
		FreezeStore: nettest.NewFreezeStore(),
		AllowList:   list,
		Edge:        netip.MustParseAddr("127.0.0.1"),
	})

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received, _ := io.Copy(io.Discard, r.Body)
		seen := r.Clone(context.Background())
		seen.ContentLength = received // what actually arrived, not what the header said
		f.seen <- seen
		_, _ = fmt.Fprintf(w, "hello from %s%s", r.Host, r.URL.Path) // #nosec G705 -- test upstream, plain text echo
	})
	plain := httptest.NewServer(handler)
	t.Cleanup(plain.Close)
	secure := httptest.NewTLSServer(handler)
	t.Cleanup(secure.Close)
	echo := listen(t)
	go serveEcho(echo)
	f.dialer = &fakeDialer{routes: map[string]string{
		allowedIP + ":80":  plain.Listener.Addr().String(),
		allowedIP + ":443": secure.Listener.Addr().String(),
		echoIP + ":443":    echo.Addr().String(),
	}}

	srv := proxy.New(f.filter, proxy.Config{Dial: f.dialer.Dial, Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	httpLn, tlsLn, proxyLn := listen(t), listen(t), listen(t)
	srv.ServeHTTP(httpLn)
	srv.ServeTLS(tlsLn)
	srv.ServeProxy(proxyLn)
	t.Cleanup(func() {
		if err := srv.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	f.httpAddr, f.tlsAddr, f.proxy = httpLn.Addr().String(), tlsLn.Addr().String(), proxyLn.Addr().String()
	return f
}

func listen(t *testing.T) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	return ln
}

func serveEcho(ln net.Listener) {
	for {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		go func() {
			defer func() { _ = c.Close() }()
			_, _ = io.Copy(c, c)
		}()
	}
}

type fakeDialer struct {
	mu     sync.Mutex
	routes map[string]string
	dialed []string
}

func (d *fakeDialer) Dial(ctx context.Context, network, addr string) (net.Conn, error) {
	d.mu.Lock()
	d.dialed = append(d.dialed, addr)
	target, ok := d.routes[addr]
	d.mu.Unlock()
	if !ok {
		return nil, errors.New("fake internet: no such host")
	}
	var nd net.Dialer
	return nd.DialContext(ctx, network, target)
}

func (d *fakeDialer) Dialed() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.dialed...)
}

// client sends every request to addr, whatever the URL says, as a transparent proxy sees it.
func client(addr string) *http.Client {
	return &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "tcp", addr)
			},
			TLSClientConfig:   &tls.Config{InsecureSkipVerify: true}, // #nosec G402 -- the test servers use a test certificate
			DisableKeepAlives: true,
		},
	}
}

func proxyClient(proxyAddr string) *http.Client {
	u := &url.URL{Scheme: "http", Host: proxyAddr}
	return &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			Proxy:             http.ProxyURL(u),
			TLSClientConfig:   &tls.Config{InsecureSkipVerify: true}, // #nosec G402 -- the test servers use a test certificate
			DisableKeepAlives: true,
		},
	}
}

func get(t *testing.T, c *http.Client, rawURL string) (int, string, error) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, rawURL, nil)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	resp, err := c.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body), err
}

func (f *fixture) neverDialed(t *testing.T, prefix string) {
	t.Helper()
	for _, d := range f.dialer.Dialed() {
		if strings.HasPrefix(d, prefix) {
			t.Fatalf("the edge dialed %s", d)
		}
	}
}

func TestHTTP_ForwardsAnAllowedHost(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	code, body, err := get(t, client(f.httpAddr), "http://"+allowedName+"/x")

	if err != nil || code != http.StatusOK || body != "hello from "+allowedName+"/x" {
		t.Fatalf("GET = %d %q, %v; want 200 from the allowed site", code, body, err)
	}
}

func TestHTTP_RefusesAHostNotOnTheList(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	code, body, err := get(t, client(f.httpAddr), "http://"+blockedName+"/")

	if err != nil || code != http.StatusForbidden || !strings.Contains(body, "allow list") {
		t.Fatalf("GET = %d %q, %v; want 403 naming the allow list", code, body, err)
	}
	f.neverDialed(t, "203.0.113.20")
}

func TestHTTP_RefusesAnIPAddressAsHost(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	code, _, err := get(t, client(f.httpAddr), "http://"+allowedIP+"/")

	if err != nil || code != http.StatusForbidden {
		t.Fatalf("GET = %d, %v; want 403", code, err)
	}
	f.neverDialed(t, allowedIP)
}

func TestHTTP_RefusesAHostWithAnotherPort(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	code, _, err := get(t, client(f.httpAddr), "http://"+allowedName+":8080/")

	if err != nil || code != http.StatusForbidden {
		t.Fatalf("GET = %d, %v; want 403", code, err)
	}
}

func TestHTTP_RecordsTheRefusal(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	_, _, _ = get(t, client(f.httpAddr), "http://"+blockedName+"/")

	got := f.observer.Events()
	if len(got) != 1 || got[0].Kind != "http" || got[0].Name != blockedName || got[0].Allowed {
		t.Fatalf("events = %+v, want one refused http record for %s", got, blockedName)
	}
}

func TestTLS_ForwardsAnAllowedServerName(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	code, body, err := get(t, client(f.tlsAddr), "https://"+allowedName+"/s")

	if err != nil || code != http.StatusOK || body != "hello from "+allowedName+"/s" {
		t.Fatalf("GET = %d %q, %v; want 200 from the allowed site", code, body, err)
	}
}

func TestTLS_ClosesAServerNameNotOnTheList(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	_, _, err := get(t, client(f.tlsAddr), "https://"+blockedName+"/")

	if err == nil {
		t.Fatal("GET to a blocked name succeeded")
	}
	f.neverDialed(t, "203.0.113.20")
}

func TestTLS_ClosesAHelloWithoutServerName(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	conn, err := tls.Dial("tcp", f.tlsAddr, &tls.Config{InsecureSkipVerify: true}) // #nosec G402 -- no name on purpose
	if err == nil {
		_ = conn.Close()
		t.Fatal("handshake without a server name succeeded")
	}
	if got := f.dialer.Dialed(); len(got) != 0 {
		t.Fatalf("the edge dialed %v", got)
	}
}

func TestTLS_ClosesBytesThatAreNotTLS(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	conn, err := net.Dial("tcp", f.tlsAddr)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))

	if _, err := conn.Write([]byte("GET / HTTP/1.1\r\nHost: " + allowedName + "\r\n\r\n")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	if _, err := io.ReadAll(conn); err != nil && !errors.Is(err, net.ErrClosed) && !strings.Contains(err.Error(), "reset") {
		t.Fatalf("ReadAll() error = %v", err)
	}
	if got := f.dialer.Dialed(); len(got) != 0 {
		t.Fatalf("the edge dialed %v", got)
	}
}

func TestConnect_TunnelsToAnAllowedName(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	code, body, err := get(t, proxyClient(f.proxy), "https://"+allowedName+"/c")

	if err != nil || code != http.StatusOK || body != "hello from "+allowedName+"/c" {
		t.Fatalf("GET = %d %q, %v; want 200 through the tunnel", code, body, err)
	}
}

func TestConnect_RefusesANameNotOnTheList(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	_, _, err := get(t, proxyClient(f.proxy), "https://"+blockedName+"/")

	if err == nil || !strings.Contains(err.Error(), "Forbidden") {
		t.Fatalf("GET error = %v, want the proxy to answer Forbidden", err)
	}
	f.neverDialed(t, "203.0.113.20")
}

func TestConnect_RefusesAPortThatIsNotWeb(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	status := rawConnect(t, f.proxy, allowedName+":22")

	if !strings.Contains(status, "403") {
		t.Fatalf("CONNECT status = %q, want 403", status)
	}
}

func TestProxy_ForwardsPlainHTTPForProxyAwareTools(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	code, body, err := get(t, proxyClient(f.proxy), "http://"+allowedName+"/plain")
	if err != nil || code != http.StatusOK || body != "hello from "+allowedName+"/plain" {
		t.Fatalf("GET = %d %q, %v; want 200 from the allowed site", code, body, err)
	}

	r := <-f.seen
	if r.RequestURI != "/plain" || !r.Close {
		t.Fatalf("upstream saw %q close=%v, want origin form /plain with Connection: close", r.RequestURI, r.Close)
	}
}

func TestProxy_ForwardsALargeRequestBody(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	body := strings.Repeat("x", 200<<10)
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "http://"+allowedName+"/upload", strings.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}

	resp, err := proxyClient(f.proxy).Do(req)
	if err != nil {
		t.Fatalf("POST error = %v", err)
	}
	_ = resp.Body.Close()

	r := <-f.seen
	if resp.StatusCode != http.StatusOK || r.ContentLength != int64(len(body)) {
		t.Fatalf("POST = %d, upstream saw %d bytes; want 200 and %d bytes", resp.StatusCode, r.ContentLength, len(body))
	}
}

func TestProxy_RefusesPlainHTTPToANameNotOnTheList(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	code, _, err := get(t, proxyClient(f.proxy), "http://"+blockedName+"/")

	if err != nil || code != http.StatusForbidden {
		t.Fatalf("GET = %d, %v; want 403", code, err)
	}
}

func TestProxy_RefusesARequestWithoutATarget(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	code, _, err := get(t, client(f.proxy), "http://"+allowedName+"/")

	if err != nil || code != http.StatusBadRequest {
		t.Fatalf("GET = %d, %v; want 400", code, err)
	}
}

func TestFreeze_CutsAnOpenTunnel(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	conn, br := openTunnel(t, f.proxy, echoName+":443")
	if got := roundTrip(t, conn, br, "ping"); got != "ping" {
		t.Fatalf("echo = %q, want ping", got)
	}

	if err := f.filter.Freeze(t.Context()); err != nil {
		t.Fatalf("Freeze() error = %v", err)
	}

	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	if _, err := br.ReadByte(); err == nil || errors.Is(err, context.DeadlineExceeded) || isTimeout(err) {
		t.Fatalf("read after freeze = %v, want the tunnel closed", err)
	}
}

func TestFreeze_RefusesNewRequests(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	if err := f.filter.Freeze(t.Context()); err != nil {
		t.Fatalf("Freeze() error = %v", err)
	}

	code, body, err := get(t, client(f.httpAddr), "http://"+allowedName+"/")

	if err != nil || code != http.StatusForbidden || !strings.Contains(body, "frozen") {
		t.Fatalf("GET = %d %q, %v; want 403 saying egress is frozen", code, body, err)
	}
}

func TestClose_CutsOpenTunnels(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	srvLn := listen(t)
	srv := proxy.New(f.filter, proxy.Config{Dial: f.dialer.Dial})
	srv.ServeProxy(srvLn)
	conn, br := openTunnel(t, srvLn.Addr().String(), echoName+":443")

	if err := srv.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	if _, err := br.ReadByte(); err == nil || isTimeout(err) {
		t.Fatalf("read after Close = %v, want the tunnel closed", err)
	}
}

func rawConnect(t *testing.T, proxyAddr, target string) string {
	t.Helper()
	conn, err := net.Dial("tcp", proxyAddr)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	if _, err := fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", target, target); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	status, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		t.Fatalf("ReadString() error = %v", err)
	}
	return status
}

func openTunnel(t *testing.T, proxyAddr, target string) (net.Conn, *bufio.Reader) {
	t.Helper()
	conn, err := net.Dial("tcp", proxyAddr)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	if _, err := fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", target, target); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, &http.Request{Method: http.MethodConnect})
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("CONNECT = %v, %v; want 200", resp, err)
	}
	_ = resp.Body.Close() // a 200 answer to CONNECT has no body; the tunnel follows
	return conn, br
}

func roundTrip(t *testing.T, conn net.Conn, br *bufio.Reader, msg string) string {
	t.Helper()
	if _, err := conn.Write([]byte(msg)); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	buf := make([]byte, len(msg))
	if _, err := io.ReadFull(br, buf); err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	return string(buf)
}

func isTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}
