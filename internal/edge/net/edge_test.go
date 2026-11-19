// SPDX-License-Identifier: MIT

package net_test

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	stdnet "net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/miekg/dns"

	"github.com/SDK-E/agentsone/internal/edge/component"
	edgenet "github.com/SDK-E/agentsone/internal/edge/net"
	"github.com/SDK-E/agentsone/internal/edge/net/nettest"
)

const (
	siteName = "registry.npmjs.org" // on the default allow list
	siteIP   = "203.0.113.30"
)

var loopback = netip.MustParseAddr("127.0.0.1")

type world struct {
	resolver *nettest.Resolver
	observer *nettest.Observer
	echo     string
}

// newWorld is a fake internet with one site that echoes on port 443.
func newWorld(t *testing.T) *world {
	t.Helper()
	ln, err := stdnet.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
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
	}()
	w := &world{resolver: nettest.NewResolver(), observer: nettest.NewObserver(), echo: ln.Addr().String()}
	w.resolver.Set(siteName, siteIP)
	return w
}

func (w *world) dial(ctx context.Context, network, addr string) (stdnet.Conn, error) {
	if addr != siteIP+":443" {
		return nil, errors.New("fake internet: no such host")
	}
	var d stdnet.Dialer
	return d.DialContext(ctx, network, w.echo)
}

func config(t *testing.T, dataDir string, w *world) edgenet.Config {
	t.Helper()
	return edgenet.Config{
		DataDir:   dataDir,
		InnerAddr: loopback,
		Ports:     &edgenet.Ports{},
		Observer:  w.observer,
		Resolver:  w.resolver,
		Dial:      w.dial,
		Log:       slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

func startEdge(t *testing.T, cfg edgenet.Config) *edgenet.Edge {
	t.Helper()
	e, err := edgenet.Start(t.Context(), cfg)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() {
		if err := e.Stop(); err != nil {
			t.Errorf("Stop() error = %v", err)
		}
	})
	return e
}

func TestStart_RefusesAnAddressThatIsNotOneInnerIPv4Address(t *testing.T) {
	t.Parallel()
	for _, addr := range []netip.Addr{{}, netip.MustParseAddr("0.0.0.0"), netip.MustParseAddr("::"), netip.MustParseAddr("::1")} {
		cfg := config(t, t.TempDir(), newWorld(t))
		cfg.InnerAddr = addr
		if e, err := edgenet.Start(t.Context(), cfg); err == nil {
			_ = e.Stop()
			t.Errorf("Start() with inner address %v succeeded", addr)
		}
	}
}

func TestStart_RefusesAnEmptyDataDir(t *testing.T) {
	t.Parallel()
	if e, err := edgenet.Start(t.Context(), config(t, "", newWorld(t))); err == nil {
		_ = e.Stop()
		t.Fatal("Start() with no data folder succeeded")
	}
}

func TestStart_BindsEveryListenerToTheInnerAddressOnly(t *testing.T) {
	t.Parallel()
	e := startEdge(t, config(t, t.TempDir(), newWorld(t)))

	a := e.Addrs()
	for name, got := range map[string]netip.AddrPort{"dns udp": a.DNSUDP, "dns tcp": a.DNSTCP, "http": a.HTTP, "tls": a.TLS, "proxy": a.Proxy} {
		if got.Addr() != loopback || got.Port() == 0 {
			t.Errorf("%s listens on %v, want %v with a port", name, got, loopback)
		}
	}
}

func TestStart_IsNotReachableOnAnotherAddressOfThisComputer(t *testing.T) {
	t.Parallel()
	other := otherLocalAddress(t)
	e := startEdge(t, config(t, t.TempDir(), newWorld(t)))
	a := e.Addrs()

	// A refused or unanswered dial both mean not reachable; some hosts drop instead of refusing,
	// so the dials run side by side with a short timeout.
	var wg sync.WaitGroup
	for name, port := range map[string]uint16{"http": a.HTTP.Port(), "tls": a.TLS.Port(), "proxy": a.Proxy.Port(), "dns tcp": a.DNSTCP.Port()} {
		wg.Go(func() {
			c, err := stdnet.DialTimeout("tcp", netip.AddrPortFrom(other, port).String(), time.Second)
			if err == nil {
				_ = c.Close()
				t.Errorf("%s is reachable on %v, want only %v", name, other, loopback)
			}
		})
	}
	wg.Wait()
}

// otherLocalAddress finds an address of this computer other than 127.0.0.1: 127.0.0.2 on Linux,
// or the address of a network card.
func otherLocalAddress(t *testing.T) netip.Addr {
	t.Helper()
	candidates := []netip.Addr{netip.MustParseAddr("127.0.0.2")}
	if addrs, err := stdnet.InterfaceAddrs(); err == nil {
		for _, a := range addrs {
			if p, err := netip.ParsePrefix(a.String()); err == nil && p.Addr().Is4() && !p.Addr().IsLoopback() {
				candidates = append(candidates, p.Addr())
			}
		}
	}
	for _, c := range candidates {
		ln, err := stdnet.Listen("tcp", netip.AddrPortFrom(c, 0).String())
		if err == nil {
			_ = ln.Close()
			return c
		}
	}
	t.Fatal("this computer has no IPv4 address other than 127.0.0.1 to prove the edge does not listen on it")
	return netip.Addr{}
}

func TestEdge_DNSAnswersAnAllowedNameWithTheInnerAddress(t *testing.T) {
	t.Parallel()
	e := startEdge(t, config(t, t.TempDir(), newWorld(t)))

	r := lookup(t, e, siteName)

	if r.Rcode != dns.RcodeSuccess || len(r.Answer) != 1 || r.Answer[0].(*dns.A).A.String() != loopback.String() {
		t.Fatalf("answer %v, want %s", r, loopback)
	}
}

func TestEdge_DNSAnswersNameErrorForABlockedName(t *testing.T) {
	t.Parallel()
	e := startEdge(t, config(t, t.TempDir(), newWorld(t)))

	r := lookup(t, e, "example.com")

	if r.Rcode != dns.RcodeNameError {
		t.Fatalf("rcode %s, want NXDOMAIN", dns.RcodeToString[r.Rcode])
	}
}

func TestEdge_TunnelsToAnAllowedName(t *testing.T) {
	t.Parallel()
	e := startEdge(t, config(t, t.TempDir(), newWorld(t)))

	conn, br, status := connect(t, e, siteName+":443")
	if status != http.StatusOK {
		t.Fatalf("CONNECT status = %d, want 200", status)
	}

	if got := echo(t, conn, br, "ping"); got != "ping" {
		t.Fatalf("echo = %q, want ping", got)
	}
}

func TestEdge_FreezeCutsAndRefusesUntilRelease(t *testing.T) {
	t.Parallel()
	e := startEdge(t, config(t, t.TempDir(), newWorld(t)))
	conn, br, _ := connect(t, e, siteName+":443")

	if err := e.Freeze(t.Context()); err != nil {
		t.Fatalf("Freeze() error = %v", err)
	}
	if _, err := br.ReadByte(); err == nil {
		t.Fatal("open tunnel survived the freeze")
	}
	_ = conn.Close()
	if _, _, status := connect(t, e, siteName+":443"); status != http.StatusForbidden {
		t.Fatalf("CONNECT while frozen = %d, want 403", status)
	}

	if err := e.Release(t.Context()); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	if _, _, status := connect(t, e, siteName+":443"); status != http.StatusOK {
		t.Fatalf("CONNECT after release = %d, want 200", status)
	}
}

func TestEdge_ComesBackFrozenAfterARestart(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	w := newWorld(t)
	first, err := edgenet.Start(t.Context(), config(t, dir, w))
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := first.Freeze(t.Context()); err != nil {
		t.Fatalf("Freeze() error = %v", err)
	}
	if err := first.Stop(); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}

	second := startEdge(t, config(t, dir, w))

	if frozen, _ := second.Frozen(t.Context()); !frozen {
		t.Fatal("edge is not frozen after a restart")
	}
}

func TestEdge_AppliesAnAllowListEditWhileRunning(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	w := newWorld(t)
	reloaded := make(chan struct{}, 1)
	cfg := config(t, dir, w)
	cfg.Log = slog.New(&signalHandler{msg: "allow list reloaded", ch: reloaded})
	e := startEdge(t, cfg)
	select { // the reload the watcher makes once it is in place
	case <-reloaded:
	case <-time.After(10 * time.Second):
		t.Fatal("the allow list watcher was not in place within 10s")
	}

	if err := edgenet.NewAllowListEditor(dir).Remove(t.Context(), siteName); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}
	select {
	case <-reloaded:
	case <-time.After(10 * time.Second):
		t.Fatal("the edge did not reload the allow list within 10s")
	}

	if r := lookup(t, e, siteName); r.Rcode != dns.RcodeNameError {
		t.Fatalf("rcode %s for a removed name, want NXDOMAIN", dns.RcodeToString[r.Rcode])
	}
}

// signalHandler is a log handler that signals ch each time msg is logged.
type signalHandler struct {
	msg string
	ch  chan struct{}
}

func (h *signalHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *signalHandler) WithAttrs([]slog.Attr) slog.Handler       { return h }
func (h *signalHandler) WithGroup(string) slog.Handler            { return h }
func (h *signalHandler) Handle(_ context.Context, r slog.Record) error {
	if r.Message == h.msg {
		select {
		case h.ch <- struct{}{}:
		default:
		}
	}
	return nil
}

func TestEdge_WritesTheDefaultAllowList(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	startEdge(t, config(t, dir, newWorld(t)))

	data, err := os.ReadFile(filepath.Join(dir, "allowlist.toml")) // #nosec G304 -- test temp folder
	if err != nil || !strings.Contains(string(data), "release-assets.githubusercontent.com") {
		t.Fatalf("allowlist.toml = %q, %v; want the default list", data, err)
	}
}

func TestStop_IsSafeTwice(t *testing.T) {
	t.Parallel()
	e, err := edgenet.Start(t.Context(), config(t, t.TempDir(), newWorld(t)))
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := e.Stop(); err != nil {
		t.Fatalf("first Stop() error = %v", err)
	}
	if err := e.Stop(); err != nil {
		t.Fatalf("second Stop() error = %v", err)
	}
}

func TestComponent_StartsTheFilterAndSharesTheFreezer(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	c := edgenet.Component{Ports: &edgenet.Ports{}, Observer: w.observer, Resolver: w.resolver, Dial: w.dial}
	env := &component.Env{InnerAddr: loopback, DataDir: t.TempDir(), Log: slog.New(slog.DiscardHandler), Admin: http.NewServeMux()}

	s, err := c.Start(t.Context(), env)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer func() { _ = s.Stop() }()

	if env.Freezer == nil {
		t.Fatal("the filter did not share its freezer")
	}
	if err := env.Freezer.Freeze(t.Context()); err != nil {
		t.Fatalf("Freeze() error = %v", err)
	}
	if frozen, _ := s.(*edgenet.Edge).Frozen(t.Context()); !frozen {
		t.Fatal("the shared freezer did not freeze the filter")
	}
	if c.Name() == "" {
		t.Error("Name() is empty")
	}
}

func TestComponent_ShowsTheFreezeSwitchOnTheAdminMux(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	c := edgenet.Component{Ports: &edgenet.Ports{}, Observer: w.observer, Resolver: w.resolver, Dial: w.dial}
	env := &component.Env{InnerAddr: loopback, DataDir: t.TempDir(), Log: slog.New(slog.DiscardHandler), Admin: http.NewServeMux()}
	s, err := c.Start(t.Context(), env)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer func() { _ = s.Stop() }()

	if err := env.Freezer.Freeze(t.Context()); err != nil {
		t.Fatalf("Freeze() error = %v", err)
	}
	rec := httptest.NewRecorder()
	env.Admin.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/v1/net/freeze", nil))

	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != `{"frozen":true}` {
		t.Fatalf("GET /v1/net/freeze = %d %s, want frozen", rec.Code, rec.Body)
	}
}

func TestComponent_RecordsRefusalsAsEdgeProof(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	proof := &proofRecorder{}
	c := edgenet.Component{Ports: &edgenet.Ports{}, Resolver: w.resolver, Dial: w.dial}
	env := &component.Env{InnerAddr: loopback, DataDir: t.TempDir(), Log: slog.New(slog.DiscardHandler), Proof: proof}
	s, err := c.Start(t.Context(), env)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer func() { _ = s.Stop() }()
	e := s.(*edgenet.Edge)

	lookup(t, e, siteName)
	lookup(t, e, "example.com")

	got := proof.all()
	if len(got) != 1 {
		t.Fatalf("proofs = %+v, want one refusal", got)
	}
	p := got[0]
	if p.Action != "net.deny" || p.Target != "network:example.com" || p.Outcome != "denied" || p.Details["reason"] != "name is not on the allow list" {
		t.Errorf("proof = %+v", p)
	}
}

type proofRecorder struct {
	mu     sync.Mutex
	proofs []component.Proof
}

func (r *proofRecorder) RecordProof(_ context.Context, p component.Proof) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.proofs = append(r.proofs, p)
	return nil
}

func (r *proofRecorder) all() []component.Proof {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]component.Proof(nil), r.proofs...)
}

func TestComponent_ReportsAStartFailure(t *testing.T) {
	t.Parallel()
	env := &component.Env{InnerAddr: netip.MustParseAddr("0.0.0.0"), DataDir: t.TempDir()}
	if _, err := (edgenet.Component{}).Start(t.Context(), env); err == nil {
		t.Fatal("Start() on 0.0.0.0 succeeded")
	}
	if env.Freezer != nil {
		t.Error("a filter that did not start shared a freezer")
	}
}

var lookupMu sync.Mutex

func lookup(t *testing.T, e *edgenet.Edge, name string) *dns.Msg {
	t.Helper()
	lookupMu.Lock()
	defer lookupMu.Unlock()
	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn(name), dns.TypeA)
	c := &dns.Client{Timeout: 5 * time.Second}
	r, _, err := c.ExchangeContext(t.Context(), m, e.Addrs().DNSUDP.String())
	if err != nil {
		t.Fatalf("Exchange() error = %v", err)
	}
	return r
}

func connect(t *testing.T, e *edgenet.Edge, target string) (stdnet.Conn, *bufio.Reader, int) {
	t.Helper()
	conn, err := stdnet.Dial("tcp", e.Addrs().Proxy.String())
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
	if err != nil {
		t.Fatalf("ReadResponse() error = %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, resp.Body) // the refusal text
	}
	_ = resp.Body.Close() // a 200 answer to CONNECT has no body; the tunnel follows
	return conn, br, resp.StatusCode
}

func echo(t *testing.T, conn stdnet.Conn, br *bufio.Reader, msg string) string {
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
