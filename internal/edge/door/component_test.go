// SPDX-License-Identifier: MIT

package door_test

import (
	"bufio"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/edge/component"
	"github.com/SDK-E/agentsone/internal/edge/door"
	"github.com/SDK-E/agentsone/internal/edge/door/adapters/listen"
)

func env(inner string, addrs ...string) *component.Env {
	return &component.Env{
		InnerAddr: netip.MustParseAddr(inner), DoorAddrs: addrs,
		Log: slog.New(slog.DiscardHandler), Admin: http.NewServeMux(),
	}
}

func machine(t *testing.T, h http.Handler) string {
	t.Helper()
	up := httptest.NewServer(h)
	t.Cleanup(up.Close)
	return up.URL
}

func startDoor(t *testing.T, c door.Component, e *component.Env) *door.Door {
	t.Helper()
	s, err := c.Start(t.Context(), e)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	d, ok := s.(*door.Door)
	if !ok {
		t.Fatalf("Start returned %T, want *door.Door", s)
	}
	return d
}

func TestDoor_ForwardsToTheMachine(t *testing.T) {
	t.Parallel()
	up := machine(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "daemon") }))
	d := startDoor(t, door.Component{Upstream: up}, env("10.77.42.2", "127.0.0.1:0"))
	t.Cleanup(func() { _ = d.Stop() })
	if len(d.Addrs()) != 1 {
		t.Fatalf("Addrs = %v, want one", d.Addrs())
	}
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+d.Addrs()[0].String()+"/", nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET through the door: %v", err)
	}
	defer func() { _ = res.Body.Close() }()
	body, _ := io.ReadAll(res.Body)
	if string(body) != "daemon" {
		t.Fatalf("body = %q, want the daemon's answer", body)
	}
}

func TestDoor_RefusesToOpenOnTheInnerAddress(t *testing.T) {
	t.Parallel()
	_, err := door.Component{}.Start(t.Context(), env("127.0.0.1", "127.0.0.1:0"))
	if !errors.Is(err, listen.ErrInnerAddr) {
		t.Fatalf("Start = %v, want ErrInnerAddr", err)
	}
}

func TestDoor_ClosesWhatItOpenedWhenALaterAddressFails(t *testing.T) {
	t.Parallel()
	_, err := door.Component{}.Start(t.Context(), env("10.77.42.2", "127.0.0.1:0", "10.77.42.2:0"))
	if !errors.Is(err, listen.ErrInnerAddr) {
		t.Fatalf("Start = %v, want ErrInnerAddr", err)
	}
}

func TestDoor_StaysClosedWithoutDoorAddresses(t *testing.T) {
	t.Parallel()
	d := startDoor(t, door.Component{}, env("10.77.42.2"))
	if len(d.Addrs()) != 0 {
		t.Fatalf("Addrs = %v, want none", d.Addrs())
	}
	if err := d.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

func TestDoor_RefusesABadUpstream(t *testing.T) {
	t.Parallel()
	for _, up := range []string{"machine:7700", "ftp://machine:7700", "http://"} {
		if _, err := (door.Component{Upstream: up}).Start(t.Context(), env("10.77.42.2", "127.0.0.1:0")); err == nil {
			t.Errorf("Start with upstream %q succeeded", up)
		}
	}
}

func TestDoor_StopClosesOpenTunnels(t *testing.T) {
	t.Parallel()
	up := machine(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		c, rw, err := http.NewResponseController(w).Hijack()
		if err != nil {
			return
		}
		defer func() { _ = c.Close() }()
		_, _ = rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n")
		_ = rw.Flush()
		_, _ = io.Copy(io.Discard, rw) // hold the tunnel open until the door closes it
	}))
	d := startDoor(t, door.Component{Upstream: up}, env("10.77.42.2", "127.0.0.1:0"))
	c, err := net.Dial("tcp", d.Addrs()[0].String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	if err := c.SetDeadline(time.Now().Add(15 * time.Second)); err != nil {
		t.Fatal(err)
	}
	_, _ = io.WriteString(c, "GET /v1/desktop/vnc HTTP/1.1\r\nHost: door\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n")
	br := bufio.NewReader(c)
	res, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatalf("upgrade: %v", err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("upgrade status = %d, want 101", res.StatusCode)
	}
	if err := d.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if _, err := br.ReadByte(); !errors.Is(err, io.EOF) {
		t.Fatalf("tunnel read after Stop = %v, want EOF", err)
	}
}
