// SPDX-License-Identifier: MIT

package net

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"path/filepath"
	"sync"
	"time"

	"github.com/SDK-E/agentsone/internal/edge/component"
	"github.com/SDK-E/agentsone/internal/edge/net/adapters/admin"
	"github.com/SDK-E/agentsone/internal/edge/net/adapters/allowfile"
	"github.com/SDK-E/agentsone/internal/edge/net/adapters/dnsserver"
	"github.com/SDK-E/agentsone/internal/edge/net/adapters/freezefile"
	"github.com/SDK-E/agentsone/internal/edge/net/adapters/proxy"
	"github.com/SDK-E/agentsone/internal/edge/net/adapters/upstream"
	"github.com/SDK-E/agentsone/internal/edge/net/app"
)

// File names in the edge data folder.
const (
	allowListFile = "allowlist.toml"
	freezeFile    = "egress-frozen"
)

// Ports are the ports the edge listens on. A zero port picks a free one, for tests.
type Ports struct {
	DNS   uint16
	HTTP  uint16
	TLS   uint16
	Proxy uint16
}

// DefaultPorts are the ports the machine reaches: DNS, transparent HTTP and TLS, and the proxy
// for proxy-aware tools.
var DefaultPorts = Ports{DNS: 53, HTTP: 80, TLS: 443, Proxy: 3128}

// Config configures the edge filter.
type Config struct {
	// DataDir holds the allow list and the freeze switch.
	DataDir string
	// InnerAddr is the edge's IPv4 address on the inner network. Every listener binds it and
	// nothing else.
	InnerAddr netip.Addr
	// Ports defaults to DefaultPorts.
	Ports *Ports
	// Observer receives every DNS query and egress decision. The default writes them to Log.
	Observer app.AuditObserver
	// Log defaults to discarding.
	Log *slog.Logger
	// Resolver looks up allowed names upstream. The default is the edge container's own resolver.
	Resolver app.Resolver
	// Dial opens outbound connections. The default is a plain dialer.
	Dial func(ctx context.Context, network, addr string) (net.Conn, error)
}

// Addrs are the addresses the edge listens on.
type Addrs struct {
	DNSUDP netip.AddrPort
	DNSTCP netip.AddrPort
	HTTP   netip.AddrPort
	TLS    netip.AddrPort
	Proxy  netip.AddrPort
}

// Edge is the running filter.
type Edge struct {
	running *app.Running
	dns     *dnsserver.Server
	proxy   *proxy.Server
	addrs   Addrs

	once    sync.Once
	stopErr error
}

var _ app.Freezer = (*Edge)(nil)

// Start checks the config, starts the filter and binds every listener on the inner address. It
// returns once DNS and the proxies accept connections.
func Start(ctx context.Context, cfg Config) (*Edge, error) {
	if err := checkInnerAddr(cfg.InnerAddr); err != nil {
		return nil, err
	}
	if cfg.DataDir == "" {
		return nil, errors.New("start edge filter: no data folder")
	}
	cfg = withDefaults(cfg)

	running, err := app.Start(ctx, app.Deps{
		Store:       allowfile.New(AllowListPath(cfg.DataDir)),
		Watcher:     allowfile.NewWatcher(AllowListPath(cfg.DataDir)),
		FreezeStore: freezefile.New(filepath.Join(cfg.DataDir, freezeFile)),
		Clock:       systemClock{},
		Resolver:    cfg.Resolver,
		Observer:    cfg.Observer,
		Log:         cfg.Log,
		Filter:      app.FilterConfig{Edge: cfg.InnerAddr},
	})
	if err != nil {
		return nil, fmt.Errorf("start edge filter: %w", err)
	}
	ls, err := listen(ctx, cfg.InnerAddr, *cfg.Ports)
	if err != nil {
		running.Stop()
		return nil, fmt.Errorf("start edge filter: %w", err)
	}

	filter := running.Filter()
	e := &Edge{
		running: running,
		dns:     dnsserver.New(filter, cfg.Log),
		proxy:   proxy.New(filter, proxy.Config{Dial: cfg.Dial, Log: cfg.Log}),
		addrs:   ls.addrs(),
	}
	e.dns.Start(ls.dnsUDP, ls.dnsTCP)
	e.proxy.ServeHTTP(ls.http)
	e.proxy.ServeTLS(ls.tls)
	e.proxy.ServeProxy(ls.proxy)
	cfg.Log.Info("edge internet filter started", "inner_addr", cfg.InnerAddr)
	return e, nil
}

// checkInnerAddr refuses anything but one IPv4 address: an unspecified address would open the
// proxies on every network, the outer one included.
func checkInnerAddr(a netip.Addr) error {
	switch {
	case !a.IsValid():
		return errors.New("start edge filter: the inner address is not set")
	case a.IsUnspecified():
		return fmt.Errorf("start edge filter: inner address %v would listen on every network", a)
	case !a.Is4():
		return fmt.Errorf("start edge filter: inner address %v is not an IPv4 address", a)
	}
	return nil
}

func withDefaults(cfg Config) Config {
	if cfg.Log == nil {
		cfg.Log = slog.New(slog.DiscardHandler)
	}
	if cfg.Ports == nil {
		p := DefaultPorts
		cfg.Ports = &p
	}
	if cfg.Observer == nil {
		cfg.Observer = logObserver{log: cfg.Log}
	}
	if cfg.Resolver == nil {
		cfg.Resolver = upstream.New(nil)
	}
	return cfg
}

// Addrs returns the addresses the edge listens on.
func (e *Edge) Addrs() Addrs { return e.addrs }

// Freeze cuts all egress and keeps it cut across restarts.
func (e *Edge) Freeze(ctx context.Context) error { return e.running.Filter().Freeze(ctx) }

// Release lets egress through again.
func (e *Edge) Release(ctx context.Context) error { return e.running.Filter().Release(ctx) }

// Frozen reports whether egress is frozen.
func (e *Edge) Frozen(ctx context.Context) (bool, error) { return e.running.Filter().Frozen(ctx) }

// Stop closes every listener and connection and stops watching the allow list. It is safe to call
// more than once.
func (e *Edge) Stop() error {
	e.once.Do(func() {
		e.stopErr = errors.Join(e.proxy.Close(), e.dns.Close())
		e.running.Stop()
	})
	return e.stopErr
}

// Component is the internet filter as a part of the edge service. Its zero value is the
// production filter on DefaultPorts; tests set the fields.
type Component struct {
	Ports    *Ports
	Observer app.AuditObserver
	Resolver app.Resolver
	Dial     func(ctx context.Context, network, addr string) (net.Conn, error)
}

var _ component.Component = Component{}

// Name names the component.
func (Component) Name() string { return "internet filter" }

// Start starts the filter on env's inner address and data folder, shares its freeze switch as
// env.Freezer, and adds the allow list and freeze routes to env.Admin.
func (c Component) Start(ctx context.Context, env *component.Env) (component.Stopper, error) {
	log := env.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	observer := c.Observer
	if observer == nil {
		observer = logObserver{log: log}
	}
	if env.Proof != nil {
		observer = proofObserver{next: observer, proof: env.Proof, log: log}
	}
	e, err := Start(ctx, Config{
		DataDir: env.DataDir, InnerAddr: env.InnerAddr, Ports: c.Ports, Log: log,
		Observer: observer, Resolver: c.Resolver, Dial: c.Dial,
	})
	if err != nil {
		return nil, err
	}
	env.Freezer = e
	if env.Admin != nil {
		admin.Register(env.Admin, NewAllowListEditor(env.DataDir), e)
	}
	return e, nil
}

// AllowListPath is the allow list file in the edge data folder.
func AllowListPath(dataDir string) string { return filepath.Join(dataDir, allowListFile) }

// NewAllowListEditor edits the allow list in dataDir. A running edge applies each saved change.
func NewAllowListEditor(dataDir string) *app.Editor {
	return app.NewEditor(allowfile.New(AllowListPath(dataDir)))
}

type listeners struct {
	dnsUDP net.PacketConn
	dnsTCP net.Listener
	http   net.Listener
	tls    net.Listener
	proxy  net.Listener
}

// listen binds every listener on addr. On failure it closes what it opened.
func listen(ctx context.Context, addr netip.Addr, p Ports) (_ *listeners, err error) {
	var lc net.ListenConfig
	ls := &listeners{}
	var opened []interface{ Close() error }
	defer func() {
		if err != nil {
			for _, c := range opened {
				_ = c.Close()
			}
		}
	}()
	tcp := func(port uint16, what string) (net.Listener, error) {
		ln, err := lc.Listen(ctx, "tcp4", netip.AddrPortFrom(addr, port).String())
		if err != nil {
			return nil, fmt.Errorf("listen for %s: %w", what, err)
		}
		opened = append(opened, ln)
		return ln, nil
	}
	if ls.dnsTCP, err = tcp(p.DNS, "DNS over TCP"); err != nil {
		return nil, err
	}
	if ls.dnsUDP, err = lc.ListenPacket(ctx, "udp4", netip.AddrPortFrom(addr, p.DNS).String()); err != nil {
		return nil, fmt.Errorf("listen for DNS over UDP: %w", err)
	}
	opened = append(opened, ls.dnsUDP)
	if ls.http, err = tcp(p.HTTP, "HTTP"); err != nil {
		return nil, err
	}
	if ls.tls, err = tcp(p.TLS, "TLS"); err != nil {
		return nil, err
	}
	if ls.proxy, err = tcp(p.Proxy, "the proxy"); err != nil {
		return nil, err
	}
	return ls, nil
}

func (ls *listeners) addrs() Addrs {
	tcp := func(ln net.Listener) netip.AddrPort { return ln.Addr().(*net.TCPAddr).AddrPort() }
	return Addrs{
		DNSUDP: ls.dnsUDP.LocalAddr().(*net.UDPAddr).AddrPort(),
		DNSTCP: tcp(ls.dnsTCP),
		HTTP:   tcp(ls.http),
		TLS:    tcp(ls.tls),
		Proxy:  tcp(ls.proxy),
	}
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

// logObserver writes every audit event to the log, so the edge container's log is the ledger of
// what the machine asked for and what was refused.
type logObserver struct{ log *slog.Logger }

func (o logObserver) Record(e app.Event) {
	o.log.Info("egress",
		"kind", e.Kind, "source", e.Source, "name", e.Name, "port", e.Port,
		"allowed", e.Allowed, "reason", e.Reason, "suppressed", e.Suppressed)
}

// proofObserver also writes each refusal to the edge audit log as proof. Allowed queries and
// connections stay in the container log only: they are too many to keep as proof.
type proofObserver struct {
	next  app.AuditObserver
	proof component.ProofRecorder
	log   *slog.Logger
}

func (o proofObserver) Record(e app.Event) {
	o.next.Record(e)
	if e.Allowed {
		return
	}
	details := map[string]any{"kind": e.Kind, "reason": e.Reason, "suppressed": e.Suppressed}
	if e.Port != 0 {
		details["port"] = int(e.Port)
	}
	if e.Source.IsValid() {
		details["source"] = e.Source.Addr().String()
	}
	target := "network:" + e.Name
	if e.Name == "" {
		target = "network:unknown"
	}
	// The filter records from request paths; the proof write must not hang on a cancelled
	// request, and a failed write must not open the wall, so it is logged and dropped.
	if err := o.proof.RecordProof(context.Background(), component.Proof{
		Action: "net.deny", Target: target, Outcome: "denied", Details: details,
	}); err != nil {
		o.log.Warn("edge proof not recorded", "action", "net.deny", "error", err)
	}
}
