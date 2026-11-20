// SPDX-License-Identifier: MIT

package socket_test

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/agents/supervisor/adapters/socket"
	"github.com/SDK-E/agentsone/internal/agents/supervisor/app"
	"github.com/SDK-E/agentsone/internal/agents/supervisor/domain"
	"github.com/SDK-E/agentsone/internal/auth/authtest"
	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/core/coretest"
	"github.com/SDK-E/agentsone/internal/gatewaymint"
	"github.com/SDK-E/agentsone/internal/gatewaytoken"
	"github.com/SDK-E/agentsone/runners/sdk"
	"github.com/SDK-E/agentsone/runners/sdk/sdktest"
)

// Token lifetimes of the credential tests: seconds, so a few dozen steps of
// fake time pass many lifetimes.
const (
	testGatewayTTL = 6 * time.Second
	testMCPTTL     = 9 * time.Second
)

// logBuffer is a log sink safe for the supervisor's goroutines and the test.
type logBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (l *logBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *logBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// countingMint is the real gateway minter that counts its mints.
type countingMint struct {
	inner core.GatewayTokens
	n     atomic.Int64
}

func (c *countingMint) Mint(ctx context.Context, g core.GatewayGrant) (string, error) {
	c.n.Add(1)
	return c.inner.Mint(ctx, g)
}

// probe is what a runner's tool and model calls found at one step.
type probe struct {
	gatewayOK, mcpOK   bool
	gatewayID, mcpID   string // short hashes of the tokens, never the tokens
	gatewayErr, mcpErr string
}

// tokenID is a short hash that tells tokens apart without holding them.
func tokenID(s sdk.Secret) string {
	sum := sha256.Sum256([]byte(s.Reveal()))
	return hex.EncodeToString(sum[:4])
}

// probeRunner is a real runner on the real SDK. It runs one long turn: on
// every step the test signals it reads its credentials like a model call and
// an MCP call would, and the real verifiers judge the tokens it holds.
type probeRunner struct {
	steps   chan struct{}
	results chan probe
	version atomic.Value // the negotiated protocol version
	creds   atomic.Pointer[sdk.Credentials]
	secrets sync.Map // every token value the runner saw, to look for in the logs
	// firstGateway and firstMCP are the welcome's tokens. They are set in
	// Start, before the first step, and read after it.
	firstGateway, firstMCP sdk.Secret
}

func (p *probeRunner) Start(_ context.Context, w sdk.Welcome) error {
	p.version.Store(w.ProtocolVersion)
	p.firstGateway, p.firstMCP = w.Model.Token, w.MCP.Token
	p.creds.Store(w.Credentials)
	return nil
}

func (p *probeRunner) Sleep(context.Context, sdk.SleepRequest) (string, error) { return "done", nil }

func (p *probeRunner) Wake(ctx context.Context, t *sdk.Turn, _ sdk.Wake) error {
	if err := t.ProfileUpdate("Goal: stay reachable. Now: calling the model and tools."); err != nil {
		return err
	}
	for range p.steps {
		c := p.creds.Load()
		gw, mcp := c.GatewayToken(), c.MCPToken()
		p.secrets.Store(gw.Reveal(), true)
		p.secrets.Store(mcp.Reveal(), true)
		select {
		case p.results <- probe{gatewayID: tokenID(gw), mcpID: tokenID(mcp)}:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return t.FinishOutput("done")
}

// daemon is the real supervisor behind a real socket, with the real gateway
// minter and the real auth issuer, all on one fake clock.
type daemon struct {
	t       *testing.T
	sup     *app.Supervisor
	srv     *socket.Server
	clock   *coretest.Clock
	rclock  *sdktest.Clock // the runners' clock, moved with the daemon's
	key     []byte
	auth    *authtest.Env
	mints   *countingMint
	logs    *logBuffer
	fakes   *coretest.Fakes
	started chan string
	wg      sync.WaitGroup
}

func newDaemon(t *testing.T) *daemon {
	t.Helper()
	dir, err := os.MkdirTemp("", "ao") // short socket path
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	key := make([]byte, gatewaymint.KeySize)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	keyFile := filepath.Join(dir, "gateway.key")
	if err := os.WriteFile(keyFile, key, 0o600); err != nil {
		t.Fatal(err)
	}

	env := authtest.New()
	fakes, ports := coretest.NewFakes()
	fakes.Agents.Add(core.Agent{Name: "founder", Kind: core.AgentAlwaysOn, IsRoot: true})
	fakes.Agents.Add(core.Agent{Name: "mira", Kind: core.AgentAlwaysOn, IsRoot: true}) // root too: the test machine has no Linux user for her
	d := &daemon{
		t: t, clock: env.Clock, rclock: sdktest.NewClock(env.Clock.Now()), key: key, auth: env,
		logs: &logBuffer{}, fakes: fakes, started: make(chan string, 8),
	}
	d.mints = &countingMint{inner: &gatewaymint.Minter{KeyFile: keyFile, Clock: env.Clock, Ports: ports}}
	ports.GatewayTokens = d.mints
	ports.Tokens = env.Service

	c := &collect{sums: map[string]app.Summary{}}
	d.srv = &socket.Server{Dir: dir, HelloDeadline: func() <-chan struct{} { return make(chan struct{}) }, Chown: func(string, int) error { return nil }}
	launcher := &socket.Launcher{Inner: &startLauncher{started: d.started}, Server: d.srv, Ctx: t.Context()}
	t.Cleanup(func() { _ = d.srv.Shutdown() })
	cfg := app.DefaultConfig()
	cfg.GatewayTokenTTL, cfg.MCPTokenTTL = testGatewayTTL, testMCPTTL
	// Heartbeats are not under test, and the runners' fake clock sends none.
	cfg.HeartbeatInterval, cfg.HeartbeatTimeout = 1000*time.Hour, 1000*time.Hour
	d.sup = app.New(app.Deps{
		Clock: env.Clock, IDs: fakes.IDs, Ports: ports, Launcher: launcher, Identity: c, Output: c, Summaries: c, Usage: c,
		Config: cfg, Wall: holdingWall{}, Log: slog.New(slog.NewTextHandler(d.logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
	})
	d.sup.CheckWall(t.Context())
	d.srv.Supervisor = d.sup
	for _, a := range []string{"founder", "mira"} {
		if err := launcher.Install(t.Context(), domain.UnitSpec{Agent: a, Account: domain.AccountFor(a, true)}); err != nil {
			t.Fatal(err)
		}
	}
	return d
}

// run wakes agent, which makes the daemon start the runner, then runs a real
// SDK runner against the agent's socket with the protocol versions it speaks.
func (d *daemon) run(agent string, versions []string) *probeRunner {
	d.t.Helper()
	p := &probeRunner{steps: make(chan struct{}), results: make(chan probe)}
	payload := fmt.Sprintf(`{"id":"msg_01JD8X3Q5Y6Z7A8B9C0D1E2F41","chatId":"cht_01JD8X3Q5Y6Z7A8B9C0D1E2F40","from":"human:alice","text":"Hello %s","sentAt":"2026-11-19T19:00:01Z"}`, agent)
	if err := d.sup.Wake(d.t.Context(), agent, core.WakeChat, json.RawMessage(payload)); err != nil {
		d.t.Fatalf("Wake(%s) error = %v", agent, err)
	}
	if got := <-d.started; got != agent {
		d.t.Fatalf("the daemon started %q, want %q", got, agent)
	}
	opts := sdk.Options{
		Runner:       sdk.Info{Name: "probe", Version: "1.1.0"},
		Capabilities: sdk.Capabilities{Streaming: true, Cancel: true, Compaction: true, Resume: true},
		Versions:     versions, Socket: d.srv.Path(agent), Clock: d.rclock,
	}
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		_ = sdk.Run(d.t.Context(), opts, p) // ends with the test: the daemon shuts down or the context ends
	}()
	d.t.Cleanup(func() {
		select {
		case <-p.steps:
		default:
			close(p.steps)
		}
	})
	return p
}

// advance moves the daemon's and the runners' time together, then lets the
// supervisor tick, which renews due credentials.
func (d *daemon) advance(by time.Duration) {
	d.t.Helper()
	d.clock.Advance(by)
	d.rclock.Advance(by)
	if err := d.sup.Tick(d.t.Context()); err != nil {
		d.t.Fatalf("Tick() error = %v", err)
	}
}

// check is a model call and an MCP call with the tokens the runner holds
// right now, judged by the real gateway token check and the real auth
// issuer at the daemon's time. The reasons never hold a token.
func (d *daemon) check(agent string, p *probeRunner) probe {
	d.t.Helper()
	select {
	case p.steps <- struct{}{}:
	case <-time.After(waitFor):
		d.t.Fatalf("%s: the turn did not take a step within %v", agent, waitFor)
	}
	var r probe
	select {
	case r = <-p.results:
	case <-time.After(waitFor):
		d.t.Fatalf("%s: the turn did not report within %v", agent, waitFor)
	}
	// The probe holds hashes only. The tokens themselves are read again here,
	// from the runner's credentials, to be verified.
	c := p.creds.Load()
	now := d.clock.Now()
	claims, err := gatewaytoken.Verify(d.key, c.GatewayToken().Reveal(), now)
	r.gatewayOK = err == nil && claims.Agent == agent
	if err != nil {
		r.gatewayErr = "the gateway refused the model token"
	}
	caller, err := d.auth.Service.Verify(d.t.Context(), c.MCPToken().Reveal())
	r.mcpOK = err == nil && caller.Name == agent && slices.Contains(caller.Scopes, app.MCPScope)
	if err != nil {
		r.mcpErr = "the MCP server refused the tool token"
	}
	return r
}

// awaitChange waits until the runner's gateway token is no longer was, so
// the credentials.update the daemon sent has been read and applied.
func awaitChange(t *testing.T, p *probeRunner, was string) {
	t.Helper()
	deadline := time.Now().Add(waitFor)
	for tokenID(p.creds.Load().GatewayToken()) == was {
		if time.Now().After(deadline) {
			t.Fatalf("the runner did not take the new gateway token within %v", waitFor)
		}
		runtime.Gosched()
	}
}

// Rule K1/K2 on the real daemon. A 1.1 runner in one long turn keeps model
// and MCP access across many token lifetimes, because the daemon renews both
// tokens before they expire. A 1.0 runner gets no update and nothing breaks.
// Tokens are minted by the real gateway minter and the real auth issuer and
// checked by the real verifiers, on a fake clock; the runner is the real SDK
// on a real Unix socket.
func TestDaemon_ARunnerKeepsModelAndToolAccessAcrossTokenRenewals(t *testing.T) {
	t.Parallel()
	d := newDaemon(t)
	fresh := d.run("founder", nil)           // the SDK default: 1.1, then 1.0
	legacy := d.run("mira", []string{"1.0"}) // a runner that only speaks 1.0
	first := d.check("founder", fresh)
	if v := fresh.version.Load(); v != "1.1" {
		t.Fatalf("the founder negotiated %v, want 1.1", v)
	}
	if v := legacy.version.Load(); v != "1.0" {
		t.Fatalf("mira negotiated %v, want 1.0", v)
	}
	if !first.gatewayOK || !first.mcpOK {
		t.Fatalf("the welcome's tokens are refused: %+v", first)
	}
	legacyFirst := d.check("mira", legacy)
	if !legacyFirst.gatewayOK || !legacyFirst.mcpOK {
		t.Fatalf("mira's welcome tokens are refused: %+v", legacyFirst)
	}

	// 40 seconds: more than six gateway lifetimes and four MCP lifetimes.
	gatewayIDs := map[string]bool{first.gatewayID: true}
	mcpIDs := map[string]bool{first.mcpID: true}
	for step := 1; step <= 40; step++ {
		before, mints := tokenID(fresh.creds.Load().GatewayToken()), d.mints.n.Load()
		d.advance(time.Second)
		if d.mints.n.Load() > mints {
			awaitChange(t, fresh, before) // the update the Tick sent is applied
		}
		r := d.check("founder", fresh)
		if !r.gatewayOK || !r.mcpOK {
			t.Fatalf("step %d (%v in): model call ok = %v, MCP call ok = %v (%s %s)",
				step, time.Duration(step)*time.Second, r.gatewayOK, r.mcpOK, r.gatewayErr, r.mcpErr)
		}
		gatewayIDs[r.gatewayID], mcpIDs[r.mcpID] = true, true
		if step < 5 { // the 1.0 runner's start tokens are still valid
			if l := d.check("mira", legacy); !l.gatewayOK || !l.mcpOK || l.gatewayID != legacyFirst.gatewayID || l.mcpID != legacyFirst.mcpID {
				t.Fatalf("step %d: mira's tokens changed or were refused", step)
			}
		}
	}
	if len(gatewayIDs) < 7 {
		t.Errorf("the runner held %d different gateway tokens in 40s, want at least 7 (one per renewal)", len(gatewayIDs))
	}
	// A token that lives seconds is renewed about once per life, not on every tick.
	if n := d.mints.n.Load(); n > 24 {
		t.Errorf("the daemon minted %d gateway tokens in 40s for 2 runners, want at most 24: renewals are not paced", n)
	}
	if len(mcpIDs) < 4 {
		t.Errorf("the runner held %d different MCP tokens in 40s, want at least 4", len(mcpIDs))
	}

	// The check is real: the welcome's tokens are long expired by now.
	if _, err := gatewaytoken.Verify(d.key, fresh.firstGateway.Reveal(), d.clock.Now()); err == nil {
		t.Error("the gateway check accepts the founder's welcome token after 40s")
	}
	if _, err := d.auth.Service.Verify(t.Context(), fresh.firstMCP.Reveal()); err == nil {
		t.Error("the MCP check accepts the founder's welcome token after 40s")
	}
	if got := tokenID(legacy.creds.Load().GatewayToken()); got != legacyFirst.gatewayID {
		t.Error("the 1.0 runner's gateway token changed: it must never get an update")
	}
	if l := d.check("mira", legacy); l.gatewayOK || l.mcpOK {
		t.Error("the 1.0 runner's start tokens still pass after 40s: the expiry is not checked")
	}

	// No token text reached the logs or the audit trail.
	audit, err := json.Marshal(d.fakes.Audit.Entries())
	if err != nil {
		t.Fatal(err)
	}
	text := d.logs.String() + string(audit)
	for _, p := range []*probeRunner{fresh, legacy} {
		p.secrets.Range(func(k, _ any) bool {
			if s, _ := k.(string); s != "" && strings.Contains(text, s) {
				t.Error("a token value is in the logs or the audit trail")
			}
			return true
		})
	}
	for _, e := range d.fakes.Audit.Entries() {
		if e.Action == app.ActionHeartbeatTimeout || e.Action == app.ActionTurnFault {
			t.Errorf("the run recorded %s on %s", e.Action, e.Target)
		}
	}
}
