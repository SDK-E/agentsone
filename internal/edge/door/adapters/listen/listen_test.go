// SPDX-License-Identifier: MIT

package listen_test

import (
	"context"
	"errors"
	"io"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/edge/door/adapters/listen"
)

var (
	loopback = netip.MustParseAddr("127.0.0.1")
	inner    = netip.MustParseAddr("10.77.42.2")
)

type fakeResolver map[string][]netip.Addr

func (f fakeResolver) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	if a, ok := f[host]; ok {
		return a, nil
	}
	return nil, errors.New("no such host")
}

func closeAll(t *testing.T, lns []net.Listener) {
	t.Helper()
	t.Cleanup(func() {
		for _, ln := range lns {
			_ = ln.Close()
		}
	})
}

func TestListen_BindsAnAddress(t *testing.T) {
	t.Parallel()
	lns, err := listen.Listen(t.Context(), "127.0.0.1:0", inner, fakeResolver{})
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	closeAll(t, lns)
	if len(lns) != 1 || netip.MustParseAddrPort(lns[0].Addr().String()).Addr() != loopback {
		t.Fatalf("listeners = %v, want one on 127.0.0.1", lns)
	}
}

func TestListen_ResolvesANameAndBindsEachAddress(t *testing.T) {
	t.Parallel()
	lns, err := listen.Listen(t.Context(), "agentsone-door:0", inner,
		fakeResolver{"agentsone-door": {loopback, netip.MustParseAddr("::1")}})
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	closeAll(t, lns)
	if len(lns) != 2 {
		t.Fatalf("listeners = %v, want two", lns)
	}
}

func TestListen_RefusesTheInnerAddress(t *testing.T) {
	t.Parallel()
	for _, addr := range []string{"127.0.0.1:0", "edge:0"} {
		_, err := listen.Listen(t.Context(), addr, loopback, fakeResolver{"edge": {loopback}})
		if !errors.Is(err, listen.ErrInnerAddr) {
			t.Errorf("Listen(%q) with inner 127.0.0.1 = %v, want ErrInnerAddr", addr, err)
		}
	}
}

func TestListen_RefusesEveryInterface(t *testing.T) {
	t.Parallel()
	for _, addr := range []string{"0.0.0.0:0", "[::]:0", ":0"} {
		if _, err := listen.Listen(t.Context(), addr, inner, fakeResolver{}); !errors.Is(err, listen.ErrInnerAddr) {
			t.Errorf("Listen(%q) = %v, want ErrInnerAddr", addr, err)
		}
	}
}

func TestListen_RefusesBadAddresses(t *testing.T) {
	t.Parallel()
	for _, addr := range []string{"no-port", "unknown:0", "127.0.0.1:http-ish"} {
		if _, err := listen.Listen(t.Context(), addr, inner, fakeResolver{}); err == nil {
			t.Errorf("Listen(%q) succeeded", addr)
		}
	}
}

func TestListen_ClosesWhatItOpenedWhenOneBindFails(t *testing.T) {
	t.Parallel()
	// 192.0.2.1 is TEST-NET-1: not on this machine, so the second bind fails.
	_, err := listen.Listen(t.Context(), "door:0", inner,
		fakeResolver{"door": {loopback, netip.MustParseAddr("192.0.2.1")}})
	if err == nil {
		t.Fatal("Listen succeeded although one address cannot be bound")
	}
}

func dial(t *testing.T, ln net.Listener) net.Conn {
	t.Helper()
	c, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func readsEOF(t *testing.T, c net.Conn) {
	t.Helper()
	if err := c.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Fatalf("read = %v, want EOF (connection closed by the door)", err)
	}
}

func TestGuard_DropsConnectionsArrivingOnTheInnerAddress(t *testing.T) {
	t.Parallel()
	raw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	g := listen.Guard(raw, loopback)
	accepted := make(chan net.Conn, 1)
	go func() {
		c, err := g.Accept()
		if err == nil {
			accepted <- c
		}
		close(accepted)
	}()
	readsEOF(t, dial(t, g))
	if err := g.Close(); err != nil {
		t.Fatal(err)
	}
	if c, ok := <-accepted; ok {
		_ = c.Close()
		t.Fatal("a connection on the inner address was handed to the server")
	}
}

func TestGuard_PassesOtherConnectionsAndClosesThemOnCloseConns(t *testing.T) {
	t.Parallel()
	raw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	g := listen.Guard(raw, inner)
	t.Cleanup(func() { _ = g.Close() })
	client := dial(t, g)
	server, err := g.Accept()
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	g.CloseConns()
	readsEOF(t, client)
	if _, err := server.Write([]byte("x")); err == nil {
		t.Fatal("server side still writable after CloseConns")
	}
}

func TestGuard_ForgetsConnectionsThatClosed(t *testing.T) {
	t.Parallel()
	raw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	g := listen.Guard(raw, inner)
	t.Cleanup(func() { _ = g.Close() })
	dial(t, g)
	server, err := g.Accept()
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	if n := g.Open(); n != 0 {
		t.Fatalf("Open = %d after close, want 0", n)
	}
}
