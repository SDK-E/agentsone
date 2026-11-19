// SPDX-License-Identifier: MIT

// Package dnsserver is the DNS server the machine uses: the edge on its inner address, port 53,
// over UDP and TCP. In the compose stack the machine's Docker resolver (127.0.0.11) answers
// service names itself and forwards every other name here. Every question goes to the filter,
// which decides the answer. The server only turns the filter's answer into DNS:
//
//   - allowed name (and gateway.agentsone.internal): A record with the edge's own inner address,
//     so the machine connects to the edge proxies and never to the internet directly;
//   - allowed name, other record type: NOERROR with no answer (NODATA), never NXDOMAIN, so a
//     client asking for AAAA first still goes on to use the A record;
//   - anything else, service records included: NXDOMAIN, plus the extended DNS error "Blocked"
//     (RFC 8914) when the client speaks EDNS. NXDOMAIN fails fast: no resolver retries or falls
//     back to another server. The audit record keeps the reason.
package dnsserver

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/netip"
	"sync"
	"time"

	"github.com/miekg/dns"

	"github.com/SDK-E/agentsone/internal/edge/net/app"
	"github.com/SDK-E/agentsone/internal/edge/net/domain"
)

// BlockedRcode is the answer code for a name that is not allowed.
const BlockedRcode = dns.RcodeNameError

const (
	queryTimeout  = 5 * time.Second
	udpAnswerSize = 1232 // the EDNS buffer size the DNS flag day 2020 settled on
	blockedText   = "name is not on the Agents One allow list"
)

// Resolver decides answers. *app.Filter is one.
type Resolver interface {
	Resolve(ctx context.Context, q app.Query) app.Answer
}

// Server serves DNS for the machine.
type Server struct {
	resolver Resolver
	log      *slog.Logger

	mu      sync.Mutex
	servers []*dns.Server
	wg      sync.WaitGroup
}

// New makes a server that asks resolver for every answer.
func New(resolver Resolver, log *slog.Logger) *Server {
	return &Server{resolver: resolver, log: log}
}

// Start serves UDP on pc and TCP on ln until Close. It returns once both are accepting queries.
func (s *Server) Start(pc net.PacketConn, ln net.Listener) {
	handler := dns.HandlerFunc(s.handle)
	s.serve(&dns.Server{PacketConn: pc, Handler: handler})
	s.serve(&dns.Server{Listener: ln, Handler: handler})
}

func (s *Server) serve(srv *dns.Server) {
	started := make(chan struct{})
	srv.NotifyStartedFunc = func() { close(started) }
	s.mu.Lock()
	s.servers = append(s.servers, srv)
	s.mu.Unlock()
	stopped := make(chan struct{})
	s.wg.Go(func() {
		defer close(stopped)
		if err := srv.ActivateAndServe(); err != nil {
			s.log.Debug("dns server stopped", "error", err)
		}
	})
	select {
	case <-started:
	case <-stopped:
	}
}

// Close stops serving and closes the connections given to Start.
func (s *Server) Close() error {
	s.mu.Lock()
	servers := s.servers
	s.servers = nil
	s.mu.Unlock()
	var errs []error
	for _, srv := range servers {
		if err := srv.Shutdown(); err != nil {
			errs = append(errs, err)
		}
	}
	s.wg.Wait()
	return errors.Join(errs...)
}

func (s *Server) handle(w dns.ResponseWriter, req *dns.Msg) {
	resp := s.answer(req, sourceOf(w.RemoteAddr()))
	size := dns.MaxMsgSize
	if _, isTCP := w.RemoteAddr().(*net.TCPAddr); !isTCP {
		size = dns.MinMsgSize
		if opt := req.IsEdns0(); opt != nil {
			size = int(min(max(opt.UDPSize(), dns.MinMsgSize), udpAnswerSize))
		}
	}
	resp.Truncate(size)
	if err := w.WriteMsg(resp); err != nil {
		s.log.Debug("dns answer not sent", "error", err)
	}
}

func (s *Server) answer(req *dns.Msg, src netip.AddrPort) *dns.Msg {
	resp := new(dns.Msg)
	resp.SetReply(req)
	resp.RecursionAvailable = true
	clientOpt := req.IsEdns0()
	if clientOpt != nil {
		resp.SetEdns0(udpAnswerSize, false)
	}

	switch {
	case req.Opcode != dns.OpcodeQuery:
		resp.Rcode = dns.RcodeNotImplemented
		return resp
	case len(req.Question) != 1:
		resp.Rcode = dns.RcodeFormatError
		return resp
	case req.Question[0].Qclass != dns.ClassINET:
		resp.Rcode = dns.RcodeRefused
		return resp
	}

	q := req.Question[0]
	ctx, cancel := context.WithTimeout(context.Background(), queryTimeout)
	defer cancel()
	ans := s.resolver.Resolve(ctx, app.Query{Source: src, Name: q.Name, Type: queryType(q.Qtype)})

	switch ans.Kind {
	case app.AnswerAddresses:
		for _, a := range ans.Addrs {
			resp.Answer = append(resp.Answer, &dns.A{
				Hdr: dns.RR_Header{Name: q.Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: ttlSeconds(ans.TTL)},
				A:   a.AsSlice(),
			})
		}
	case app.AnswerNoData:
	default:
		resp.Rcode = BlockedRcode
		if clientOpt != nil {
			opt := resp.IsEdns0()
			opt.Option = append(opt.Option, &dns.EDNS0_EDE{InfoCode: dns.ExtendedErrorCodeBlocked, ExtraText: blockedText})
		}
	}
	return resp
}

func queryType(t uint16) domain.QueryType {
	switch t {
	case dns.TypeA:
		return domain.QueryA
	case dns.TypeAAAA:
		return domain.QueryAAAA
	default:
		return domain.QueryOther
	}
}

func ttlSeconds(d time.Duration) uint32 {
	s := d / time.Second
	if s < 0 {
		return 0
	}
	return uint32(min(s, 1<<31-1)) // #nosec G115 -- clamped above
}

func sourceOf(addr net.Addr) netip.AddrPort {
	switch a := addr.(type) {
	case *net.UDPAddr:
		return a.AddrPort()
	case *net.TCPAddr:
		return a.AddrPort()
	default:
		return netip.AddrPort{}
	}
}
