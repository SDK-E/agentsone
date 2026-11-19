// SPDX-License-Identifier: MIT

// Package listen opens the web door's listeners on the outer side of the edge, never on the inner
// address the machine reaches.
//
// Two guards, both deny by default:
//   - Listen refuses a door address that is, or resolves to, the inner address, and refuses the
//     unspecified address (every interface, the inner one included).
//   - Guard drops any connection whose local address is the inner address, in case a listener
//     ever ends up reachable from the machine anyway.
package listen

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"sync"
)

// ErrInnerAddr is returned for a door address that would listen where the machine can reach it.
var ErrInnerAddr = errors.New("door: refusing to listen on the inner address or on every interface")

// Resolver looks up a door address's host name. *net.Resolver is one.
type Resolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

// Listen opens one TCP listener per address of addr (host:port; the host may be a name, which is
// resolved once, now). Each listener is guarded. If any address is refused or cannot be bound,
// nothing stays open.
func Listen(ctx context.Context, addr string, inner netip.Addr, r Resolver) ([]net.Listener, error) {
	host, portText, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("door address %q: %w", addr, err)
	}
	port, err := strconv.ParseUint(portText, 10, 16)
	if err != nil {
		return nil, fmt.Errorf("door address %q: bad port", addr)
	}
	ips, err := resolve(ctx, host, r)
	if err != nil {
		return nil, fmt.Errorf("door address %q: %w", addr, err)
	}
	for _, ip := range ips {
		if ip.IsUnspecified() || ip == inner.Unmap() {
			return nil, fmt.Errorf("door address %q (%s): %w", addr, ip, ErrInnerAddr)
		}
	}
	var lc net.ListenConfig
	lns := make([]net.Listener, 0, len(ips))
	for _, ip := range ips {
		ln, err := lc.Listen(ctx, "tcp", netip.AddrPortFrom(ip, uint16(port)).String())
		if err != nil {
			for _, open := range lns {
				_ = open.Close()
			}
			return nil, fmt.Errorf("door address %q: %w", addr, err)
		}
		lns = append(lns, Guard(ln, inner))
	}
	return lns, nil
}

func resolve(ctx context.Context, host string, r Resolver) ([]netip.Addr, error) {
	if host == "" {
		return []netip.Addr{netip.IPv4Unspecified()}, nil
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		return []netip.Addr{ip.Unmap()}, nil
	}
	ips, err := r.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, err
	}
	out := make([]netip.Addr, 0, len(ips))
	for _, ip := range ips {
		out = append(out, ip.Unmap())
	}
	return out, nil
}

// Guarded is a listener that drops connections arriving on the inner address and tracks the
// connections it handed out, so the door can close hijacked ones (WebSocket, streams) on stop.
type Guarded struct {
	net.Listener
	inner netip.Addr
	mu    sync.Mutex
	conns map[*conn]struct{}
}

// Guard wraps ln.
func Guard(ln net.Listener, inner netip.Addr) *Guarded {
	return &Guarded{Listener: ln, inner: inner.Unmap(), conns: map[*conn]struct{}{}}
}

// Accept returns the next connection that did not arrive on the inner address.
func (g *Guarded) Accept() (net.Conn, error) {
	for {
		c, err := g.Listener.Accept()
		if err != nil {
			return nil, err
		}
		if local, ok := addrOf(c.LocalAddr()); ok && local != g.inner {
			tc := &conn{Conn: c, g: g}
			g.mu.Lock()
			g.conns[tc] = struct{}{}
			g.mu.Unlock()
			return tc, nil
		}
		_ = c.Close()
	}
}

func addrOf(a net.Addr) (netip.Addr, bool) {
	ap, err := netip.ParseAddrPort(a.String())
	if err != nil {
		return netip.Addr{}, false
	}
	return ap.Addr().Unmap(), true
}

// CloseConns closes every connection still open.
func (g *Guarded) CloseConns() {
	g.mu.Lock()
	open := make([]*conn, 0, len(g.conns))
	for c := range g.conns {
		open = append(open, c)
	}
	g.mu.Unlock()
	for _, c := range open {
		_ = c.Close()
	}
}

// Open counts the connections still open.
func (g *Guarded) Open() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.conns)
}

type conn struct {
	net.Conn
	g    *Guarded
	once sync.Once
}

func (c *conn) Close() error {
	c.once.Do(func() {
		c.g.mu.Lock()
		delete(c.g.conns, c)
		c.g.mu.Unlock()
	})
	return c.Conn.Close()
}
