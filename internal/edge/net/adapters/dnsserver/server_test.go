// SPDX-License-Identifier: MIT

package dnsserver_test

import (
	"io"
	"log/slog"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/miekg/dns"

	"github.com/SDK-E/agentsone/internal/edge/net/adapters/dnsserver"
	"github.com/SDK-E/agentsone/internal/edge/net/app"
	"github.com/SDK-E/agentsone/internal/edge/net/domain"
	"github.com/SDK-E/agentsone/internal/edge/net/nettest"
)

const edgeIP = "172.30.0.2"

type serverFixture struct {
	observer *nettest.Observer
	udpAddr  string
	tcpAddr  string
}

func startServer(t *testing.T) serverFixture {
	t.Helper()
	f := serverFixture{observer: nettest.NewObserver()}
	github, _ := domain.ParsePattern("github.com")
	list, _ := domain.NewAllowList([]domain.Entry{{Pattern: github}})
	filter := app.NewFilter(app.FilterConfig{
		Clock:       nettest.NewClock(time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)),
		Resolver:    nettest.NewResolver(),
		Observer:    f.observer,
		FreezeStore: nettest.NewFreezeStore(),
		AllowList:   list,
		Edge:        netip.MustParseAddr(edgeIP),
	})
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenPacket() error = %v", err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	srv := dnsserver.New(filter, slog.New(slog.NewTextHandler(io.Discard, nil)))
	srv.Start(pc, ln)
	t.Cleanup(func() { _ = srv.Close() })
	f.udpAddr, f.tcpAddr = pc.LocalAddr().String(), ln.Addr().String()
	return f
}

func exchange(t *testing.T, network, addr string, m *dns.Msg) *dns.Msg {
	t.Helper()
	c := &dns.Client{Net: network, Timeout: 5 * time.Second}
	r, _, err := c.ExchangeContext(t.Context(), m, addr)
	if err != nil {
		t.Fatalf("Exchange() error = %v", err)
	}
	return r
}

func query(name string, qtype uint16) *dns.Msg {
	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn(name), qtype)
	return m
}

func TestServer_AnswersAllowedNameWithTheEdgeAddress(t *testing.T) {
	t.Parallel()
	f := startServer(t)

	r := exchange(t, "udp", f.udpAddr, query("github.com", dns.TypeA))

	if r.Rcode != dns.RcodeSuccess || len(r.Answer) != 1 {
		t.Fatalf("rcode %s with %d answers, want NOERROR with one", dns.RcodeToString[r.Rcode], len(r.Answer))
	}
	a, ok := r.Answer[0].(*dns.A)
	if !ok || a.A.String() != edgeIP || a.Hdr.Ttl != 60 || a.Hdr.Name != "github.com." {
		t.Fatalf("answer %v, want github.com. 60 IN A %s", r.Answer[0], edgeIP)
	}
}

func TestServer_AnswersOverTCP(t *testing.T) {
	t.Parallel()
	f := startServer(t)

	r := exchange(t, "tcp", f.tcpAddr, query("github.com", dns.TypeA))

	if r.Rcode != dns.RcodeSuccess || len(r.Answer) != 1 {
		t.Fatalf("rcode %s with %d answers, want NOERROR with one", dns.RcodeToString[r.Rcode], len(r.Answer))
	}
}

func TestServer_RefusesBlockedName(t *testing.T) {
	t.Parallel()
	f := startServer(t)

	r := exchange(t, "udp", f.udpAddr, query("example.com", dns.TypeA))

	if r.Rcode != dnsserver.BlockedRcode || len(r.Answer) != 0 {
		t.Fatalf("rcode %s with %d answers, want %s with none", dns.RcodeToString[r.Rcode], len(r.Answer), dns.RcodeToString[dnsserver.BlockedRcode])
	}
}

func TestServer_RefusesServiceRecordsOfAnAllowedSiteName(t *testing.T) {
	t.Parallel()
	f := startServer(t)

	r := exchange(t, "udp", f.udpAddr, query("_http._tcp.github.com", dns.TypeSRV))

	if r.Rcode != dnsserver.BlockedRcode {
		t.Fatalf("rcode %s, want %s", dns.RcodeToString[r.Rcode], dns.RcodeToString[dnsserver.BlockedRcode])
	}
}

func TestServer_SaysWhyANameIsBlockedWhenTheClientSpeaksEDNS(t *testing.T) {
	t.Parallel()
	f := startServer(t)
	m := query("example.com", dns.TypeA)
	m.SetEdns0(1232, false)

	r := exchange(t, "udp", f.udpAddr, m)

	opt := r.IsEdns0()
	if opt == nil {
		t.Fatal("no OPT record in the answer")
	}
	for _, o := range opt.Option {
		if ede, ok := o.(*dns.EDNS0_EDE); ok && ede.InfoCode == dns.ExtendedErrorCodeBlocked {
			return
		}
	}
	t.Fatalf("OPT options %v, want extended error Blocked", opt.Option)
}

func TestServer_AddsNoOptRecordWhenTheClientSentNone(t *testing.T) {
	t.Parallel()
	f := startServer(t)

	r := exchange(t, "udp", f.udpAddr, query("example.com", dns.TypeA))

	if r.IsEdns0() != nil {
		t.Fatal("answer has an OPT record the client did not ask for")
	}
}

func TestServer_RecordsEveryQueryWithItsSource(t *testing.T) {
	t.Parallel()
	f := startServer(t)

	exchange(t, "udp", f.udpAddr, query("github.com", dns.TypeA))
	exchange(t, "udp", f.udpAddr, query("example.com", dns.TypeA))

	got := f.observer.Events()
	if len(got) != 2 || !got[0].Allowed || got[1].Allowed || got[1].Name != "example.com" || !got[1].Source.Addr().IsLoopback() {
		t.Fatalf("events = %+v, want github.com allowed then example.com refused, from loopback", got)
	}
}

func TestServer_AnswersNoDataForIPv6OfAllowedName(t *testing.T) {
	t.Parallel()
	f := startServer(t)

	r := exchange(t, "udp", f.udpAddr, query("github.com", dns.TypeAAAA))

	if r.Rcode != dns.RcodeSuccess || len(r.Answer) != 0 {
		t.Fatalf("rcode %s with %d answers, want NOERROR with none", dns.RcodeToString[r.Rcode], len(r.Answer))
	}
}

func TestServer_RejectsMalformedRequests(t *testing.T) {
	t.Parallel()
	twoQuestions := query("github.com", dns.TypeA)
	twoQuestions.Question = append(twoQuestions.Question, dns.Question{Name: "example.com.", Qtype: dns.TypeA, Qclass: dns.ClassINET})
	notify := query("github.com", dns.TypeSOA)
	notify.Opcode = dns.OpcodeNotify
	chaos := query("github.com", dns.TypeTXT)
	chaos.Question[0].Qclass = dns.ClassCHAOS
	tests := []struct {
		name  string
		msg   *dns.Msg
		rcode int
	}{
		{"two questions", twoQuestions, dns.RcodeFormatError},
		{"not a query", notify, dns.RcodeNotImplemented},
		{"not the internet class", chaos, dns.RcodeRefused},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := startServer(t)

			r := exchange(t, "udp", f.udpAddr, tt.msg)

			if r.Rcode != tt.rcode {
				t.Fatalf("rcode %s, want %s", dns.RcodeToString[r.Rcode], dns.RcodeToString[tt.rcode])
			}
		})
	}
}
