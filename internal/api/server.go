// SPDX-License-Identifier: MIT

package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"time"

	httpadapter "github.com/SDK-E/agentsone/internal/api/adapters/http"
	"github.com/SDK-E/agentsone/internal/api/adapters/peer"
	"github.com/SDK-E/agentsone/internal/api/domain"
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
	unchecked, err := Compose(mods)
	if err != nil {
		return nil, err
	}
	// Token scopes are checked around the composed modules, not per listener,
	// so in-process dispatch (machine MCP tool calls) is checked too.
	composed := httpadapter.RequireScopes(unchecked)
	var edge EdgeSettings
	if err := deps.Config.Section("edge", &edge); err != nil {
		return nil, fmt.Errorf("edge settings: %w", err)
	}
	edgeIPs, err := parseIPs(edge.TrustedProxies)
	if err != nil {
		return nil, err
	}
	public, err := parsePublicURL(deps.Config.API.PublicURL)
	if err != nil {
		return nil, err
	}
	ports := deps.Ports
	verify := func(ctx context.Context, v string) (core.Caller, error) { return ports.Tokens.Verify(ctx, v) }
	ui := httpadapter.UI(os.DirFS(filepath.Join(deps.Config.LibDir, "ui")))
	web := httpadapter.SecurityHeaders(httpadapter.Split(composed, ui))
	s := &Server{
		composed: composed,
		tcp: httpadapter.RequestLog(deps.Log, "tcp", deps.Clock,
			httpadapter.TCPIdentity{Verify: verify, EdgeIPs: edgeIPs, IsOwn: ownAddress, Public: public}.Wrap(web)),
		resolver: peer.Resolver{
			Proc: peer.ProcFS(), Credentials: peer.Credentials, DaemonPID: os.Getpid(),
			Directory: domain.Directory{AgentByUID: agentByUID(ports), HumanByUID: humanByUID(ports), HumanByName: humanByName(ports)},
		},
		log: deps.Log,
		cfg: *deps.Config,
	}
	refused := func(r *http.Request, err error) { s.refused(r, ports, err) }
	s.socket = httpadapter.RequestLog(deps.Log, "socket", deps.Clock, httpadapter.SocketIdentity(verify, refused, web))
	return s, nil
}

// ActionSocketRefused is the audit action of a socket peer that is not an
// agent, a human or the daemon.
const ActionSocketRefused = "api.socket.refused"

// refused records a refused socket peer in the audit trail: who (uid,
// pid), why, and what it asked for.
func (s *Server) refused(r *http.Request, ports *core.Ports, err error) {
	p, reason := domain.Peer{PID: -1, UID: -1}, err.Error()
	var ref *domain.Refusal
	if errors.As(err, &ref) {
		p, reason = ref.Peer, ref.Reason
	}
	s.log.Info("api: socket peer refused", "pid", p.PID, "uid", p.UID, "reason", reason, "err", err)
	detail, jerr := json.Marshal(map[string]any{
		"pid": p.PID, "uid": p.UID, "reason": reason, "method": r.Method, "path": r.URL.Path,
	})
	if jerr != nil {
		s.log.Error("api: refused socket peer not audited", "uid", p.UID, "err", jerr)
		return
	}
	entry := core.AuditEntry{
		Actor: "system:api", Action: ActionSocketRefused, Target: "uid:" + strconv.Itoa(p.UID),
		Detail: detail, Outcome: core.AuditDenied,
	}
	if aerr := ports.Audit.Append(r.Context(), entry); aerr != nil {
		s.log.Error("api: refused socket peer not audited", "uid", p.UID, "err", aerr)
	}
}

// Handler is the composed modules with no identity rules, behind the token
// scope check. In-process callers (the MCP module) dispatch into it with
// core.WithCaller already set on the context; it never derives identity
// itself, and a token's caller is held to its scopes here too.
func (s *Server) Handler() http.Handler { return s.composed }

// SocketHandler is the Unix socket chain, for tests.
func (s *Server) SocketHandler() http.Handler { return s.socket }

// TCPHandler is the TCP chain, for tests.
func (s *Server) TCPHandler() http.Handler { return s.tcp }

// agentByUID finds a custom agent by uid in the agents directory, read
// when each socket connection is accepted. A failed read is an error, never
// "no agent": the resolver then refuses the peer.
func agentByUID(ports *core.Ports) func(context.Context, int) (string, error) {
	return func(ctx context.Context, uid int) (string, error) {
		all, err := ports.Agents.ListAgents(ctx)
		if err != nil {
			return "", fmt.Errorf("list agents: %w", err)
		}
		for _, a := range all {
			if a.UID == uid && !a.IsRoot {
				return a.Name, nil
			}
		}
		return "", fmt.Errorf("agent with uid %d: %w", uid, core.ErrNotFound)
	}
}

// humanByUID finds the human whose Linux user has uid, through the humans
// port that auth owns.
func humanByUID(ports *core.Ports) func(context.Context, int) (string, error) {
	return func(ctx context.Context, uid int) (string, error) {
		h, err := ports.Humans.HumanByUID(ctx, uid)
		if err != nil {
			return "", fmt.Errorf("human with uid %d: %w", uid, err)
		}
		return h.Name, nil
	}
}

// humanByName confirms a human called name exists, through the humans port
// that auth owns. A process in /humans/<name> is that human only then.
func humanByName(ports *core.Ports) func(context.Context, string) error {
	return func(ctx context.Context, name string) error {
		if _, err := ports.Humans.HumanAccount(ctx, name); err != nil {
			return fmt.Errorf("human %s: %w", name, err)
		}
		return nil
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

// parsePublicURL reads [api] public_url: an http or https origin, scheme and
// host only. Empty means none is configured.
func parsePublicURL(s string) (*url.URL, error) {
	if s == "" {
		return nil, nil
	}
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil ||
		(u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("[api] public_url %q must be an http or https origin such as https://agents.example.com", s)
	}
	return &url.URL{Scheme: u.Scheme, Host: u.Host}, nil
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

// Listen opens the Unix socket and the TCP address. The socket is mode 0666
// in a 0755 folder, both root's: agents, humans' Linux users and the daemon
// run as many uids, so the file mode cannot tell them apart. The kernel
// tells us who connects (SO_PEERCRED) and the resolver refuses the rest.
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
		return nil, errors.Join(fmt.Errorf("open socket to local callers: %w", err), unixLn.Close())
	}
	tcpLn, err := net.Listen("tcp", s.cfg.API.Addr)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("listen on %s: %w", s.cfg.API.Addr, err), unixLn.Close())
	}
	socketSrv := &http.Server{
		Handler: s.socket, ReadHeaderTimeout: readHeaderTimeout,
		ConnContext: s.resolver.ConnContext(httpadapter.WithPeer),
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
