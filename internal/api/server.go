// SPDX-License-Identifier: MIT

package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"time"

	httpadapter "github.com/SDK-E/agentsone/internal/api/adapters/http"
	"github.com/SDK-E/agentsone/internal/api/adapters/peer"
	"github.com/SDK-E/agentsone/internal/core"
)

// EdgeSettings is the [edge] table of config.toml.
type EdgeSettings struct {
	// TrustedProxies are addresses or CIDR prefixes of the edge reverse
	// proxy. Only they may reach the TCP listener from off the loopback,
	// and only their X-Forwarded-* headers are read.
	TrustedProxies []string `toml:"trusted_proxies"`
}

// Server is the API layer: the composed modules behind the identity rules
// of each listener.
type Server struct {
	composed http.Handler
	socket   http.Handler
	tcp      http.Handler
	resolver peer.Resolver
	log      *slog.Logger
	cfg      core.Config
}

// NewServer composes mods and builds the per-listener handlers. Tokens and
// session cookies are checked through deps.Ports.Tokens, read per request.
func NewServer(mods []core.Module, deps core.Deps) (*Server, error) {
	composed, err := Compose(mods)
	if err != nil {
		return nil, err
	}
	var edge EdgeSettings
	if err := deps.Config.Section("edge", &edge); err != nil {
		return nil, fmt.Errorf("edge settings: %w", err)
	}
	edgeIPs, err := parseIPs(edge.TrustedProxies)
	if err != nil {
		return nil, err
	}
	ports := deps.Ports
	verify := func(ctx context.Context, v string) (core.Caller, error) { return ports.Tokens.Verify(ctx, v) }
	ui := httpadapter.UI(os.DirFS(filepath.Join(deps.Config.LibDir, "ui")))
	web := httpadapter.SecurityHeaders(httpadapter.Split(composed, ui))
	s := &Server{
		composed: composed,
		socket:   httpadapter.RequestLog(deps.Log, "socket", deps.Clock, httpadapter.SocketIdentity(verify, web)),
		tcp: httpadapter.RequestLog(deps.Log, "tcp", deps.Clock,
			httpadapter.TCPIdentity{Verify: verify, EdgeIPs: edgeIPs, IsOwn: ownAddress}.Wrap(web)),
		resolver: peer.Resolver{Proc: peer.ProcFS(), Credentials: peer.Credentials, AgentByUID: agentByUID(ports)},
		log:      deps.Log,
		cfg:      *deps.Config,
	}
	return s, nil
}

// Handler is the composed modules with no identity rules. In-process
// callers (the MCP module) dispatch into it with core.WithCaller already
// set on the context; it never derives identity itself.
func (s *Server) Handler() http.Handler { return s.composed }

// SocketHandler is the Unix socket chain, for tests.
func (s *Server) SocketHandler() http.Handler { return s.socket }

// TCPHandler is the TCP chain, for tests.
func (s *Server) TCPHandler() http.Handler { return s.tcp }

// agentByUID finds a custom agent by uid in the agents directory, read
// when each socket connection is accepted.
func agentByUID(ports *core.Ports) func(int) (string, bool) {
	return func(uid int) (string, bool) {
		all, err := ports.Agents.ListAgents(context.Background())
		if err != nil {
			return "", false
		}
		for _, a := range all {
			if a.UID == uid && !a.IsRoot {
				return a.Name, true
			}
		}
		return "", false
	}
}

// parseIPs reads exact edge addresses. Ranges are refused: the edge is one
// known peer, not a network.
func parseIPs(in []string) ([]netip.Addr, error) {
	out := make([]netip.Addr, 0, len(in))
	for _, s := range in {
		a, err := netip.ParseAddr(s)
		if err != nil {
			return nil, fmt.Errorf("edge trusted proxy %q must be one IP address: %w", s, err)
		}
		out = append(out, a.Unmap())
	}
	return out, nil
}

// ownAddress reports whether a is one of this machine's interface
// addresses, looked up per request so address changes are seen.
func ownAddress(a netip.Addr) bool {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return false
	}
	for _, x := range addrs {
		if p, err := netip.ParsePrefix(x.String()); err == nil && p.Addr().Unmap() == a {
			return true
		}
	}
	return false
}

// Running is a server with open listeners.
type Running struct {
	servers []*http.Server
	lns     []net.Listener
	log     *slog.Logger
}

const readHeaderTimeout = 10 * time.Second

// Listen opens the Unix socket (mode 0666: every local process may
// connect, the kernel tells us who it is) and the TCP address.
func (s *Server) Listen() (*Running, error) {
	sock := s.cfg.SocketPath()
	if err := os.MkdirAll(filepath.Dir(sock), 0o755); err != nil { // #nosec G301 -- agents of other users must reach the socket
		return nil, fmt.Errorf("create socket folder: %w", err)
	}
	if err := os.Remove(sock); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("remove stale socket: %w", err)
	}
	unixLn, err := net.Listen("unix", sock)
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", sock, err)
	}
	if err := os.Chmod(sock, 0o666); err != nil { // #nosec G302 -- identity comes from SO_PEERCRED, not file mode
		_ = unixLn.Close() // already failing; the chmod error is the one to report
		return nil, fmt.Errorf("open socket to local callers: %w", err)
	}
	tcpLn, err := net.Listen("tcp", s.cfg.API.Addr)
	if err != nil {
		_ = unixLn.Close() // already failing; the listen error is the one to report
		return nil, fmt.Errorf("listen on %s: %w", s.cfg.API.Addr, err)
	}
	socketSrv := &http.Server{
		Handler: s.socket, ReadHeaderTimeout: readHeaderTimeout,
		ConnContext: s.resolver.ConnContext(httpadapter.WithPeerCaller),
	}
	tcpSrv := &http.Server{Handler: s.tcp, ReadHeaderTimeout: readHeaderTimeout}
	return &Running{servers: []*http.Server{socketSrv, tcpSrv}, lns: []net.Listener{unixLn, tcpLn}, log: s.log}, nil
}

// Addrs are the listening addresses, socket first.
func (r *Running) Addrs() []net.Addr {
	out := make([]net.Addr, 0, len(r.lns))
	for _, ln := range r.lns {
		out = append(out, ln.Addr())
	}
	return out
}

// Serve starts serving; the channel gets each listener's end.
func (r *Running) Serve() <-chan error {
	errs := make(chan error, len(r.lns))
	for i, ln := range r.lns {
		r.log.Info("listening", "addr", ln.Addr().String())
		go func() { errs <- r.servers[i].Serve(ln) }()
	}
	return errs
}

// Shutdown stops every listener gracefully.
func (r *Running) Shutdown(ctx context.Context) error {
	var errs []error
	for _, srv := range r.servers {
		if err := srv.Shutdown(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("shut down: %w", err)
	}
	return nil
}
