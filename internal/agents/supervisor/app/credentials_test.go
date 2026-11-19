// SPDX-License-Identifier: MIT

package app_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/SDK-E/agentsone/internal/agents/supervisor/app"
	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/runners/sdk/protocol"
)

// twoThirds is two thirds of the default 10 minute gateway token life.
const twoThirds = 6*time.Minute + 40*time.Second

type credentialsUpdate struct {
	GatewayToken          string    `json:"gatewayToken"`
	GatewayTokenExpiresAt time.Time `json:"gatewayTokenExpiresAt"`
	McpToken              string    `json:"mcpToken"`
	McpTokenExpiresAt     time.Time `json:"mcpTokenExpiresAt"`
}

// updates returns the credentials.update notifications the runner got.
func updates(t *testing.T, conn *fakeConn) []credentialsUpdate {
	t.Helper()
	var out []credentialsUpdate
	for _, n := range conn.notified() {
		if n.method != protocol.MethodCredentialsUpdate {
			t.Fatalf("notification %s, want only %s", n.method, protocol.MethodCredentialsUpdate)
		}
		var u credentialsUpdate
		if err := json.Unmarshal(n.params, &u); err != nil {
			t.Fatal(err)
		}
		out = append(out, u)
	}
	return out
}

// welcomeMCP is the MCP token and expiry of a welcome.
func welcomeMCP(t *testing.T, res protocol.HelloResult) (string, time.Time) {
	t.Helper()
	m, ok := res.Mcp.(protocol.McpEndpointHttp)
	if !ok {
		t.Fatalf("mcp endpoint %T, want streamable-http", res.Mcp)
	}
	return string(m.Token), time.Time(m.TokenExpiresAt)
}

// pass moves the fake clock by d, keeps the runner heard so its heartbeat
// does not run out, and ticks the supervisor.
func (w *world) pass(t *testing.T, h *app.Handler, d time.Duration) {
	t.Helper()
	w.fakes.Clock.Advance(d)
	h.Heard()
	if err := w.sup.Tick(t.Context()); err != nil {
		t.Fatalf("Tick() error = %v", err)
	}
}

// connect11 wakes agent and plays a runner that negotiated 1.1.
func (w *world) connect11(t *testing.T, agent string) (*app.Handler, *fakeConn, protocol.HelloResult) {
	t.Helper()
	if err := w.sup.Wake(t.Context(), agent, core.WakeChat, json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	return w.connectAt(t, agent, "1.1")
}

func TestCredentials_A11RunnerGetsAFreshGatewayTokenAtTwoThirdsOfItsLife(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	h, conn, res := w.connect11(t, "mira")
	start := w.fakes.Clock.Now()
	mcpToken, mcpExpires := welcomeMCP(t, res)

	w.pass(t, h, twoThirds-time.Second)
	if got := updates(t, conn); len(got) != 0 {
		t.Fatalf("update sent %s early: %+v", time.Second, got)
	}
	minted := len(w.fakes.GatewayTokens.Grants())
	w.pass(t, h, time.Second)

	want := []credentialsUpdate{{
		GatewayToken: "fake-gateway-token-mira", GatewayTokenExpiresAt: start.Add(twoThirds + 10*time.Minute),
		McpToken: mcpToken, McpTokenExpiresAt: mcpExpires,
	}}
	if diff := cmp.Diff(want, updates(t, conn)); diff != "" {
		t.Fatalf("updates (-want +got):\n%s", diff)
	}
	if got := len(w.fakes.GatewayTokens.Grants()); got != minted+1 {
		t.Fatalf("gateway tokens minted for the update = %d, want 1", got-minted)
	}

	// Once per token: the next update comes two thirds into the new one's life.
	w.pass(t, h, 0)
	w.pass(t, h, twoThirds-time.Second)
	if got := updates(t, conn); len(got) != 1 {
		t.Fatalf("updates before the new token's two thirds = %d, want 1", len(got))
	}
	w.pass(t, h, time.Second)
	if got := updates(t, conn); len(got) != 2 {
		t.Fatalf("updates = %d, want 2", len(got))
	}
}

// Rule K1: a 1.0 runner keeps the tokens of its welcome.
func TestCredentials_A10RunnerGetsNone(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	_ = w.sup.Wake(t.Context(), "mira", core.WakeChat, json.RawMessage(`{}`))
	h, conn, _ := w.connect(t, "mira")
	minted := len(w.fakes.GatewayTokens.Grants())

	for range 30 {
		w.pass(t, h, time.Minute)
	}

	if got := updates(t, conn); len(got) != 0 || len(w.fakes.GatewayTokens.Grants()) != minted {
		t.Fatalf("1.0 runner got %d updates and %d new tokens, want none", len(got), len(w.fakes.GatewayTokens.Grants())-minted)
	}
}

// Rule H8 and D4: nothing goes to the runner before its first status, so
// an update is never sent before the welcome.
func TestCredentials_NothingBeforeTheFirstStatus(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	_ = w.sup.Wake(t.Context(), "mira", core.WakeChat, json.RawMessage(`{}`))
	h := w.sup.Connect("mira")
	conn := newFakeConn()
	h.Attach(conn)
	if _, err := h.Hello(t.Context(), "1.1", helloParams); err != nil {
		t.Fatal(err)
	}

	w.pass(t, h, twoThirds)
	if got := updates(t, conn); len(got) != 0 {
		t.Fatalf("update before the first status: %+v", got)
	}
	if err := h.Status(t.Context(), protocol.StatusParams{State: "idle"}); err != nil {
		t.Fatal(err)
	}
	conn.markReady() // the read loop, once Status has returned
	w.pass(t, h, 0)
	if got := updates(t, conn); len(got) != 1 {
		t.Fatalf("updates once ready = %d, want the overdue one", len(got))
	}
}

// The session is ready as soon as Status accepts the first status, but the
// connection only once Status returns (rule H8). An update due in that gap
// waits for the connection instead of being refused.
func TestCredentials_WaitUntilTheConnectionIsReady(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	_ = w.sup.Wake(t.Context(), "mira", core.WakeChat, json.RawMessage(`{}`))
	h := w.sup.Connect("mira")
	conn := newFakeConn()
	h.Attach(conn)
	if _, err := h.Hello(t.Context(), "1.1", helloParams); err != nil {
		t.Fatal(err)
	}
	if err := h.Status(t.Context(), protocol.StatusParams{State: "idle"}); err != nil {
		t.Fatal(err)
	}
	w.fakes.Clock.Advance(twoThirds)
	h.Heard()
	ticked := make(chan error, 1)
	go func() { ticked <- w.sup.Tick(t.Context()) }()

	conn.markReadyAfter(t, 2) // the held wake and the update

	if err := <-ticked; err != nil {
		t.Fatalf("Tick() error = %v", err)
	}
	if got := updates(t, conn); len(got) != 1 {
		t.Fatalf("updates = %d, want 1", len(got))
	}
}

// A connection that ends before it is ready gets no update and no call:
// they return the connection's error instead of waiting.
func TestCredentials_ConnectionEndingBeforeReadyGetsNothing(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	_ = w.sup.Wake(t.Context(), "mira", core.WakeChat, json.RawMessage(`{}`))
	h := w.sup.Connect("mira")
	conn := newFakeConn()
	h.Attach(conn)
	if _, err := h.Hello(t.Context(), "1.1", helloParams); err != nil {
		t.Fatal(err)
	}
	if err := h.Status(t.Context(), protocol.StatusParams{State: "idle"}); err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()

	w.pass(t, h, twoThirds)
	if got := updates(t, conn); len(got) != 0 {
		t.Fatalf("updates on an ended connection = %+v", got)
	}
	if err := w.sup.Sleep(t.Context(), "mira", true); !errors.Is(err, errFakeClosed) {
		t.Fatalf("Sleep() error = %v, want the connection's error", err)
	}
}

// D5: when no gateway token can be minted, nothing is sent, the runner
// keeps its tokens, and the supervisor tries again every 30 seconds.
func TestCredentials_MintFailureSendsNothingAndRetriesEvery30Seconds(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	h, conn, res := w.connect11(t, "mira")
	w.fakes.GatewayTokens.Err = fmt.Errorf("model gateway not reachable: %w", core.ErrUnavailable)

	w.pass(t, h, twoThirds)
	if got := updates(t, conn); len(got) != 0 {
		t.Fatalf("update sent without a gateway token: %+v", got)
	}
	w.fakes.GatewayTokens.Err = nil
	w.pass(t, h, 29*time.Second)
	if got := updates(t, conn); len(got) != 0 {
		t.Fatalf("retried before 30 seconds: %+v", got)
	}
	w.pass(t, h, time.Second)
	if got := updates(t, conn); len(got) != 1 {
		t.Fatalf("updates after the retry = %d, want 1", len(got))
	}

	logs := w.logs.text()
	if !strings.Contains(logs, "credentials") || !strings.Contains(logs, "model gateway not reachable") {
		t.Errorf("the failed renewal is not logged:\n%s", logs)
	}
	mcpToken, _ := welcomeMCP(t, res)
	for name, secret := range map[string]string{"gateway token": "fake-gateway-token", "MCP token": mcpToken} {
		if strings.Contains(logs, secret) {
			t.Errorf("logs hold the %s", name)
		}
	}
}

// D6: a closed connection gets no more updates.
func TestCredentials_StopWhenTheConnectionCloses(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	h, conn, _ := w.connect11(t, "mira")
	minted := len(w.fakes.GatewayTokens.Grants())
	h.Closed(errors.New("runner exited"))

	w.pass(t, h, 20*time.Minute)

	if got := updates(t, conn); len(got) != 0 || len(w.fakes.GatewayTokens.Grants()) != minted {
		t.Fatalf("closed connection got %d updates", len(got))
	}
}

// D3: the MCP token is resent unchanged until two thirds of its own life,
// then a fresh one goes with the next update.
func TestCredentials_MCPTokenIsResentUntilTwoThirdsOfItsOwnLife(t *testing.T) {
	t.Parallel()
	w := newWorldWith(t, func(c *app.Config) { c.MCPTokenTTL = 15 * time.Minute })
	h, conn, res := w.connect11(t, "mira")
	start := w.fakes.Clock.Now()

	w.pass(t, h, twoThirds) // MCP token at 6m40s of 15m: keep it
	w.pass(t, h, twoThirds) // at 13m20s, past its 10m: a fresh one

	got := updates(t, conn)
	if len(got) != 2 {
		t.Fatalf("updates = %d, want 2", len(got))
	}
	mcpToken, mcpExpires := welcomeMCP(t, res)
	if got[0].McpToken != mcpToken || !got[0].McpTokenExpiresAt.Equal(mcpExpires) {
		t.Errorf("first update MCP token = %+v, want the welcome's", got[0])
	}
	if got[1].McpToken == mcpToken || !got[1].McpTokenExpiresAt.Equal(start.Add(2*twoThirds+15*time.Minute)) {
		t.Errorf("second update MCP token = %+v, want a fresh one", got[1])
	}
}

// K1: an update goes at least 60 seconds before the earliest expiry, even
// when the MCP token lives shorter than the gateway token.
func TestCredentials_SentAtLeastAMinuteBeforeTheEarliestExpiry(t *testing.T) {
	t.Parallel()
	w := newWorldWith(t, func(c *app.Config) { c.MCPTokenTTL = 2 * time.Minute })
	h, conn, res := w.connect11(t, "mira")

	w.pass(t, h, 59*time.Second)
	if got := updates(t, conn); len(got) != 0 {
		t.Fatalf("update at 59s: %+v", got)
	}
	w.pass(t, h, time.Second) // 2/3 of 2m is 80s, but 60s before expiry is 60s
	got := updates(t, conn)
	if mcpToken, _ := welcomeMCP(t, res); len(got) != 1 || got[0].McpToken == mcpToken {
		t.Fatalf("updates = %+v, want one with a fresh MCP token", got)
	}
}
