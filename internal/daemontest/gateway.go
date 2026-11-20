// SPDX-License-Identifier: MIT

package daemontest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/agents/supervisor/app"
	"github.com/SDK-E/agentsone/internal/contract/api"
	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/core/coretest"
	budgetstore "github.com/SDK-E/agentsone/internal/edge/budget/adapters/sqlite"
	budgetapp "github.com/SDK-E/agentsone/internal/edge/budget/app"
	ledger "github.com/SDK-E/agentsone/internal/edge/budget/domain"
	"github.com/SDK-E/agentsone/internal/edge/gateway"
	gatewayconfig "github.com/SDK-E/agentsone/internal/edge/gateway/adapters/config"
	"github.com/SDK-E/agentsone/internal/edge/gateway/adapters/keyfile"
	"github.com/SDK-E/agentsone/internal/gatewaymint"
	"github.com/SDK-E/agentsone/internal/gatewaytoken"
)

// Option changes how Start runs the daemon.
type Option func(*startup)

// startup is what the options may change before the daemon starts.
type startup struct {
	t   testing.TB
	cfg *core.Config
	// cfgPath is config.toml; addSection adds module tables to it.
	cfgPath string
	clock   *coretest.Clock
	// supervisor is the supervisor's settings; nil keeps its defaults.
	supervisor *app.Config
	// ready run once the daemon serves, in order.
	ready []func(*Daemon)
}

// The model the harness's gateway offers: one provider with one model per
// weight, so a test can see which weight a call asked for.
const (
	// ModelProvider is the provider's id in the gateway's config.
	ModelProvider = "provider"
	// ModelQuick, ModelNormal and ModelDeep are the provider's models, by
	// the weight runners ask for.
	ModelQuick  = "provider-quick"
	ModelNormal = "provider-normal"
	ModelDeep   = "provider-deep"
	// modelBudget is the gateway's whole budget, far above what a test spends.
	modelBudget = 100 * ledger.MicrosPerUSD
)

// WithModelProvider puts the real model gateway (internal/edge/gateway)
// between the agents and an OpenAI-compatible provider at baseURL (with its
// /v1), whose key is key. The gateway is the one the edge runs: its config,
// key store, budget and token check are the real ones, on a loopback
// listener. Runners get its address in their welcome, as on a machine, and
// the daemon signs their gateway tokens with the gateway's own key.
//
// Runners also get the API's real tool server (core.MCPPath), so a model's
// tool calls reach the machine. The openai runner runs in process. The
// router makes its rating call to the same gateway.
//
// The provider's models refuse the request fields the default gateway.toml
// lists for the Claude model of the same tier, so the gateway drops them as
// it does on a machine.
//
// The harness ships no provider: a test brings its own, such as the fake
// model in this package's tests.
func WithModelProvider(baseURL, key string) Option {
	return func(s *startup) {
		s.t.Helper()
		gw := startGateway(s.t, s.clock, baseURL, key)
		s.addSection("router", fmt.Sprintf("gateway_url = %q", "http://"+gw.addr))
		// The edge hands its token key to the daemon, which signs the
		// runners' tokens with it.
		var settings gatewaymint.Settings
		if err := s.cfg.Section("gateway", &settings); err != nil {
			s.t.Fatalf("read the [gateway] settings: %v", err)
		}
		if err := os.WriteFile(settings.KeyFile, gw.TokenKey(), 0o600); err != nil {
			s.t.Fatalf("hand the gateway key to the daemon: %v", err)
		}
		tools := forward(s.t)
		cfg := app.DefaultConfig()
		cfg.GatewayURL = "http://" + gw.addr + gatewaytoken.BasePath
		cfg.MCPURL = "http://" + tools.addr() + core.MCPPath
		s.supervisor = &cfg
		s.ready = append(s.ready, func(d *Daemon) { tools.to(d.TCPAddr()) })
	}
}

// addSection adds the module table [name] with body to config.toml and loads
// the file again, the way the daemon reads it.
func (s *startup) addSection(name, body string) {
	s.t.Helper()
	old, err := os.ReadFile(s.cfgPath)
	if err != nil {
		s.t.Fatalf("read config.toml: %v", err)
	}
	text := fmt.Sprintf("%s\n[%s]\n%s\n", old, name, body)
	// The path is config.toml in the test's own temporary folder.
	if err := os.WriteFile(s.cfgPath, []byte(text), 0o600); err != nil { //nolint:gosec // G703: see above
		s.t.Fatalf("write config.toml: %v", err)
	}
	cfg, err := core.LoadConfig(s.cfgPath)
	if err != nil {
		s.t.Fatalf("load config.toml with [%s]: %v", name, err)
	}
	*s.cfg = cfg
}

// claudeDrops are the request fields the default gateway.toml drops for its
// Claude model of tier.
func claudeDrops(t testing.TB, tier string) []string {
	t.Helper()
	for _, p := range gatewayconfig.Default().Providers {
		for _, m := range p.Models {
			if m.Family == "claude" && m.Tier == tier {
				return m.DropParams
			}
		}
	}
	t.Fatalf("the default gateway.toml has no Claude model of tier %s", tier)
	return nil
}

// gatewayRun is the gateway serving on a loopback listener.
type gatewayRun struct {
	*gateway.Server
	addr string
}

// startGateway writes the gateway's config, key and budget the way the edge
// keeps them, opens the gateway on clock and serves it until the test ends.
func startGateway(t testing.TB, clock *coretest.Clock, baseURL, key string) *gatewayRun {
	t.Helper()
	dir := t.TempDir()
	cfg := gatewayconfig.File{
		Limits: gatewayconfig.Default().Limits,
		Providers: []gatewayconfig.Provider{{
			ID: ModelProvider, Enabled: true, OpenAIBaseURL: baseURL, KeyRequired: true,
			Models: []gatewayconfig.Model{
				{ID: ModelQuick, Tier: "quick", Family: "provider", Input: "0.10", Output: "0.50", DropParams: claudeDrops(t, "quick")},
				{ID: ModelNormal, Tier: "normal", Family: "provider", Input: "1.00", Output: "2.00", DropParams: claudeDrops(t, "normal")},
				{ID: ModelDeep, Tier: "deep", Family: "provider", Input: "4.00", Output: "20.00", DropParams: claudeDrops(t, "deep")},
			},
		}},
	}
	if err := gatewayconfig.Save(filepath.Join(dir, gatewayconfig.FileName), cfg); err != nil {
		t.Fatalf("write gateway.toml: %v", err)
	}
	ctx := context.Background()
	keys, err := keyfile.Open(filepath.Join(dir, keyfile.FolderName))
	if err != nil {
		t.Fatalf("open the gateway's key store: %v", err)
	}
	if err := keys.Set(ctx, ModelProvider, key); err != nil {
		t.Fatalf("store the provider key: %v", err)
	}
	budget, err := budgetstore.Open(ctx, filepath.Join(dir, gateway.BudgetFileName))
	if err != nil {
		t.Fatalf("open the gateway budget: %v", err)
	}
	if err := budget.SaveSettings(ctx, budgetapp.Settings{Total: modelBudget}); err != nil {
		t.Fatalf("set the gateway budget: %v", err)
	}
	if err := budget.Close(); err != nil {
		t.Fatalf("close the gateway budget: %v", err)
	}
	srv, err := gateway.Open(ctx, gateway.Options{
		DataDir: dir,
		Frozen:  func(context.Context) (bool, error) { return false, nil },
		Clock:   clock,
	})
	if err != nil {
		t.Fatalf("open the gateway: %v", err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		coretest.Close(t, srv)
		t.Fatalf("listen for the gateway: %v", err)
	}
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	// No ticks: the config stays as written and the budget is flushed when
	// the gateway stops.
	go func() { done <- srv.Run(runCtx, make(chan time.Time), ln) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("the gateway stopped with an error: %v", err)
		}
	})
	return &gatewayRun{Server: srv, addr: ln.Addr().String()}
}

// forwarder carries runners' tool calls to the API's TCP listener. The
// supervisor needs the address before the daemon starts and the listener's
// port is known only once it serves, so the forwarder listens first and
// connects each call once the daemon is ready.
type forwarder struct {
	ln     net.Listener
	target chan struct{} // closed once the address is first set
	once   sync.Once

	mu    sync.Mutex
	dest  string
	conns map[net.Conn]struct{} // nil once closed
	errs  []error
	wg    sync.WaitGroup
}

func forward(t testing.TB) *forwarder {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for the tool server: %v", err)
	}
	f := &forwarder{ln: ln, target: make(chan struct{}), conns: map[net.Conn]struct{}{}}
	f.wg.Add(1)
	go f.serve()
	t.Cleanup(func() { f.close(t) })
	return f
}

func (f *forwarder) addr() string { return f.ln.Addr().String() }

// to sets where calls go: the API's address of the daemon that runs now. A
// restarted daemon listens on a new port and sets it again.
func (f *forwarder) to(addr string) {
	f.mu.Lock()
	f.dest = addr
	f.mu.Unlock()
	f.once.Do(func() { close(f.target) })
}

func (f *forwarder) serve() {
	defer f.wg.Done()
	for {
		c, err := f.ln.Accept()
		if err != nil {
			return
		}
		f.wg.Add(1)
		go f.pipe(c)
	}
}

// pipe copies one connection both ways until either side ends. A call that
// comes while no daemon listens, such as during a restart, is cut, as a
// runner would see on a machine.
func (f *forwarder) pipe(c net.Conn) {
	defer f.wg.Done()
	if !f.track(c) {
		return
	}
	defer f.shut(c)
	<-f.target
	f.mu.Lock()
	dest := f.dest
	f.mu.Unlock()
	if dest == "" {
		return // the forwarder closed before any daemon was ready
	}
	up, err := (&net.Dialer{Timeout: Timeout}).Dial("tcp", dest)
	if err != nil {
		return
	}
	if !f.track(up) {
		return
	}
	defer f.shut(up)
	done := make(chan struct{}, 2)
	// A copy ends when either side closes or resets, which is the end of
	// the call, not a fault: the runner and the daemon see their own errors.
	go func() { _, _ = io.Copy(up, c); done <- struct{}{} }()
	go func() { _, _ = io.Copy(c, up); done <- struct{}{} }()
	<-done
	// Closing both sides ends the other copy.
	f.shut(c)
	f.shut(up)
	<-done
}

// track keeps c to close at the end, or cuts it at once when the forwarder
// is already closed.
func (f *forwarder) track(c net.Conn) bool {
	f.mu.Lock()
	closed := f.conns == nil
	if !closed {
		f.conns[c] = struct{}{}
	}
	f.mu.Unlock()
	if closed {
		f.shut(c)
	}
	return !closed
}

// shut closes c. Closing a connection that is already closed is no fault;
// any other close error fails the test at the end.
func (f *forwarder) shut(c net.Conn) {
	err := c.Close()
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.conns != nil {
		delete(f.conns, c)
	}
	if err != nil && !errors.Is(err, net.ErrClosed) {
		f.errs = append(f.errs, err)
	}
}

// close stops listening, cuts every call and waits for the copies.
func (f *forwarder) close(t testing.TB) {
	if err := f.ln.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		t.Errorf("close the tool server forwarder: %v", err)
	}
	f.mu.Lock()
	open := make([]net.Conn, 0, len(f.conns))
	for c := range f.conns {
		open = append(open, c)
	}
	f.conns = nil
	f.mu.Unlock()
	for _, c := range open {
		f.shut(c)
	}
	f.once.Do(func() { close(f.target) }) // never set: calls waiting for it give up
	f.wg.Wait()
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, err := range f.errs {
		t.Errorf("close a forwarded tool call: %v", err)
	}
}

// HireRunner hires a chat agent named name that runs runner (echo or
// openai), below the founder. The client must be an agent allowed to hire,
// such as the founder. An openai agent needs WithModelProvider.
func (h *Client) HireRunner(name, runner string) api.Agent {
	h.d.t.Helper()
	founder := "founder"
	var a api.Agent
	h.Do(http.MethodPost, "/agents", api.AgentCreate{
		Name: name, DisplayName: name, Kind: api.AgentKindChat, Role: "Answers through the model gateway",
		Runner: runner, Manager: &founder,
	}, &a)
	return a
}
