// SPDX-License-Identifier: MIT

// Package proxy holds the edge egress proxies. The machine has no route to the internet; its DNS
// answers every allowed name with the edge's own inner address, so its connections land here:
//
//   - port 80, transparent HTTP: the target is the Host header of the first request;
//   - port 443, transparent TLS: the target is the server name in the TLS ClientHello, read
//     without terminating TLS (the edge never sees the plaintext and holds no certificate);
//   - port 3128, for proxy-aware tools (HTTP_PROXY, HTTPS_PROXY): CONNECT host:port tunnels and
//     plain absolute-form HTTP requests.
//
// Each request goes to the filter, which refuses names off the allow list, IP addresses, other
// ports and everything while frozen, and otherwise returns the public addresses the edge itself
// resolved for that name. The proxy connects only to those addresses, replays the bytes it read,
// and then copies both ways. Every open connection is registered with the filter so a freeze cuts
// it at once.
//
// Honest limits: on a kept-alive plain HTTP connection through port 80, later requests may name
// another Host; they still go to the address resolved for the first, allowed name. A TLS client
// using Encrypted ClientHello is routed by its outer server name. Neither reaches an address the
// edge did not resolve for an allowed name.
package proxy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"sync"
	"time"

	"github.com/SDK-E/agentsone/internal/edge/net/app"
	"github.com/SDK-E/agentsone/internal/edge/net/domain"
)

const (
	defaultHandshakeTimeout = 10 * time.Second
	defaultDialTimeout      = 15 * time.Second
	maxHead                 = 64 << 10
)

// Router decides egress requests and tracks open connections. *app.Filter is one.
type Router interface {
	Route(ctx context.Context, r app.EgressRequest) (app.Route, error)
	Track(c io.Closer) (release func(), ok bool)
}

// Config configures the proxies. Zero fields take the defaults.
type Config struct {
	// Dial opens outbound connections. It is only ever called with an IP address and port the
	// router returned. The default is a net.Dialer with a 15 second timeout.
	Dial func(ctx context.Context, network, addr string) (net.Conn, error)
	// Reachable reports whether the proxies may connect to an address. It is checked again
	// right before each dial, whatever the router returned, so a fault in the router cannot
	// open a non-public or denied destination. The default allows public addresses only
	// (domain.IsPublicDestination).
	Reachable func(netip.Addr) bool
	Log       *slog.Logger
	// HandshakeTimeout bounds how long a client may take to send its request head or TLS hello.
	HandshakeTimeout time.Duration
}

// Server runs the three egress proxies.
type Server struct {
	router           Router
	dial             func(ctx context.Context, network, addr string) (net.Conn, error)
	reachable        func(netip.Addr) bool
	log              *slog.Logger
	handshakeTimeout time.Duration
	ctx              context.Context
	cancel           context.CancelFunc
	wg               sync.WaitGroup

	mu        sync.Mutex
	listeners []net.Listener
	conns     map[net.Conn]struct{}
	closed    bool
}

// New makes the proxies. Start each with its Serve method.
func New(router Router, cfg Config) *Server {
	if cfg.Dial == nil {
		d := &net.Dialer{Timeout: defaultDialTimeout}
		cfg.Dial = d.DialContext
	}
	if cfg.Reachable == nil {
		cfg.Reachable = domain.IsPublicDestination
	}
	if cfg.Log == nil {
		cfg.Log = slog.New(slog.DiscardHandler)
	}
	if cfg.HandshakeTimeout == 0 {
		cfg.HandshakeTimeout = defaultHandshakeTimeout
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Server{
		router: router, dial: cfg.Dial, reachable: cfg.Reachable, log: cfg.Log, handshakeTimeout: cfg.HandshakeTimeout,
		ctx: ctx, cancel: cancel, conns: map[net.Conn]struct{}{},
	}
}

// ServeHTTP runs the transparent HTTP proxy on ln until Close.
func (s *Server) ServeHTTP(ln net.Listener) { s.serve(ln, s.handleHTTP) }

// ServeTLS runs the transparent TLS proxy on ln until Close.
func (s *Server) ServeTLS(ln net.Listener) { s.serve(ln, s.handleTLS) }

// ServeProxy runs the proxy for proxy-aware tools on ln until Close.
func (s *Server) ServeProxy(ln net.Listener) { s.serve(ln, s.handleProxy) }

func (s *Server) serve(ln net.Listener, handle func(net.Conn)) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		_ = ln.Close() //nolint:errcheck // the server is closed, so the listener is turned away unused
		return
	}
	s.listeners = append(s.listeners, ln)
	s.mu.Unlock()
	s.wg.Go(func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			if !s.track(conn) {
				return
			}
			s.wg.Go(func() {
				defer s.untrack(conn)
				handle(conn)
			})
		}
	})
}

// Close stops every proxy, closes every connection and waits for them to end.
func (s *Server) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	listeners := s.listeners
	for c := range s.conns {
		_ = c.Close() //nolint:errcheck // cutting connections at close; a failed close leaves nothing to undo
	}
	s.mu.Unlock()
	s.cancel()
	var errs []error
	for _, ln := range listeners {
		if err := ln.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			errs = append(errs, err)
		}
	}
	s.wg.Wait()
	return errors.Join(errs...)
}

func (s *Server) track(c net.Conn) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		_ = c.Close() //nolint:errcheck // the server is closed, so the connection is turned away unread
		return false
	}
	s.conns[c] = struct{}{}
	return true
}

func (s *Server) untrack(c net.Conn) {
	s.mu.Lock()
	delete(s.conns, c)
	s.mu.Unlock()
	_ = c.Close() //nolint:errcheck // the connection is over; its handler already reported what failed
}

// route asks the router about host:port for conn's client.
func (s *Server) route(conn net.Conn, host string, port uint16, via domain.Via) (app.Route, error) {
	ctx, cancel := context.WithTimeout(s.ctx, defaultDialTimeout)
	defer cancel()
	return s.router.Route(ctx, app.EgressRequest{Source: sourceOf(conn), Host: host, Port: port, Via: via})
}

// connect dials the first address of the route that answers. An address that is not reachable
// is never dialed: the router should not have returned it, so it is logged as an error.
func (s *Server) connect(route app.Route) (net.Conn, error) {
	var errs []error
	for _, addr := range route.Addrs {
		if !s.reachable(addr.Addr()) {
			s.log.Error("egress refused at dial: the address is not public or is denied", "name", route.Name, "addr", addr)
			errs = append(errs, fmt.Errorf("%s is not a reachable destination", addr))
			continue
		}
		ctx, cancel := context.WithTimeout(s.ctx, defaultDialTimeout)
		c, err := s.dial(ctx, "tcp", addr.String())
		cancel()
		if err == nil {
			return c, nil
		}
		errs = append(errs, err)
	}
	return nil, fmt.Errorf("connect to %s: %w", route.Name, errors.Join(errs...))
}

// splice writes first to upstream, then copies both ways until both sides are done. The pair is
// registered with the router, so a freeze closes it.
func (s *Server) splice(client net.Conn, clientReader io.Reader, upstream net.Conn, first []byte) {
	both := closePair{client, upstream}
	release, ok := s.router.Track(both)
	if !ok {
		return
	}
	defer release()
	defer func() { _ = both.Close() }() //nolint:errcheck // the tunnel is over; closing both ends leaves nothing to report
	_ = client.SetDeadline(time.Time{}) //nolint:errcheck // only a closed connection fails this, and then the copies below end at once
	if len(first) > 0 {
		if _, err := upstream.Write(first); err != nil {
			return
		}
	}
	var wg sync.WaitGroup
	wg.Go(func() {
		_, _ = io.Copy(upstream, clientReader) //nolint:errcheck // a copy ends when either side closes; that is the tunnel's normal end
		closeWrite(upstream)
	})
	wg.Go(func() {
		_, _ = io.Copy(client, upstream) //nolint:errcheck // a copy ends when either side closes; that is the tunnel's normal end
		closeWrite(client)
	})
	wg.Wait()
}

type closePair [2]net.Conn

func (p closePair) Close() error { return errors.Join(p[0].Close(), p[1].Close()) }

func closeWrite(c net.Conn) {
	if cw, ok := c.(interface{ CloseWrite() error }); ok {
		_ = cw.CloseWrite() //nolint:errcheck // half-close after the copy; the deferred Close of both ends follows
		return
	}
	_ = c.Close() //nolint:errcheck // no half-close here, so the end closes; the deferred Close of both ends follows
}

func sourceOf(c net.Conn) netip.AddrPort {
	if a, ok := c.RemoteAddr().(*net.TCPAddr); ok {
		return a.AddrPort()
	}
	return netip.AddrPort{}
}

// headLimit caps how much may be read before the request head is parsed. lift removes the cap
// for the body, which may be of any size.
type headLimit struct {
	r      io.Reader
	n      int
	lifted bool
}

func (h *headLimit) Read(p []byte) (int, error) {
	if h.lifted {
		return h.r.Read(p)
	}
	if h.n >= maxHead {
		return 0, errors.New("request head too large")
	}
	n, err := h.r.Read(p[:min(len(p), maxHead-h.n)])
	h.n += n
	return n, err
}

func (h *headLimit) lift() { h.lifted = true }

// recorder keeps every byte read through it, so what was read to make a decision can be
// replayed to the upstream unchanged.
type recorder struct {
	r   io.Reader
	buf []byte
}

func (rec *recorder) Read(p []byte) (int, error) {
	if len(rec.buf) >= maxHead {
		return 0, errors.New("request head too large")
	}
	n, err := rec.r.Read(p[:min(len(p), maxHead-len(rec.buf))])
	rec.buf = append(rec.buf, p[:n]...)
	return n, err
}
