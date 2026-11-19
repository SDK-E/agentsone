// SPDX-License-Identifier: MIT

package upstream_test

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"slices"
	"testing"

	"github.com/miekg/dns"

	"github.com/SDK-E/agentsone/internal/edge/net/adapters/upstream"
	"github.com/SDK-E/agentsone/internal/edge/net/app"
)

// fakeUpstream is a local DNS server that knows one name; it stands in for the host's resolver
// so the test never touches the internet.
func fakeUpstream(t *testing.T) *net.Resolver {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenPacket() error = %v", err)
	}
	started := make(chan struct{})
	srv := &dns.Server{PacketConn: pc, NotifyStartedFunc: func() { close(started) }, Handler: dns.HandlerFunc(func(w dns.ResponseWriter, r *dns.Msg) {
		m := new(dns.Msg)
		m.SetReply(r)
		q := r.Question[0]
		switch {
		case q.Name == "github.com." && q.Qtype == dns.TypeA:
			rr, _ := dns.NewRR("github.com. 60 IN A 140.82.121.4")
			m.Answer = append(m.Answer, rr)
		case q.Name == "github.com.":
		case q.Name == "broken.example.":
			m.Rcode = dns.RcodeServerFailure
		default:
			m.Rcode = dns.RcodeNameError
		}
		_ = w.WriteMsg(m)
	})}
	done := make(chan struct{})
	go func() { defer close(done); _ = srv.ActivateAndServe() }()
	<-started
	t.Cleanup(func() { _ = srv.Shutdown(); <-done })
	addr := pc.LocalAddr().String()
	return &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "udp", addr)
	}}
}

func TestResolver_ReturnsIPv4Addresses(t *testing.T) {
	t.Parallel()
	r := upstream.New(fakeUpstream(t))

	got, err := r.LookupIPv4(t.Context(), "github.com")

	if err != nil || !slices.Equal(got, []netip.Addr{netip.MustParseAddr("140.82.121.4")}) {
		t.Fatalf("LookupIPv4() = %v, %v; want [140.82.121.4]", got, err)
	}
}

func TestResolver_ReportsAMissingNameAsNotFound(t *testing.T) {
	t.Parallel()
	r := upstream.New(fakeUpstream(t))

	_, err := r.LookupIPv4(t.Context(), "missing.example")

	if !errors.Is(err, app.ErrNameNotFound) {
		t.Fatalf("LookupIPv4() error = %v, want %v", err, app.ErrNameNotFound)
	}
}

func TestResolver_ReportsUpstreamFailureAsAnotherError(t *testing.T) {
	t.Parallel()
	r := upstream.New(fakeUpstream(t))

	_, err := r.LookupIPv4(t.Context(), "broken.example")

	if err == nil || errors.Is(err, app.ErrNameNotFound) {
		t.Fatalf("LookupIPv4() error = %v, want a failure that is not %v", err, app.ErrNameNotFound)
	}
}
