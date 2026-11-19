// SPDX-License-Identifier: MIT

package sdk_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/runners/sdk"
)

// Made-up tokens. They are long enough for the ScopedToken schema (16 to 4096
// characters), so only the case under test makes an update invalid.
const (
	freshGateway = "fresh-gateway-token-0002"
	freshMCP     = "fresh-mcp-token-00000002"
	farFuture    = "2099-01-01T00:00:00Z"
	longAgo      = "2000-01-01T00:00:00Z"
)

// welcome11 is the rig welcome with protocol 1.1 chosen.
func welcome11() string {
	return strings.Replace(welcomeJSON, `"protocolVersion":"1.0"`, `"protocolVersion":"1.1"`, 1)
}

// credentialsLine is a credentials.update notification with these params.
func credentialsLine(params string) string {
	return `{"jsonrpc":"2.0","method":"credentials.update","params":` + params + `}`
}

func credentialsParams(gw, gwAt, mcp, mcpAt string) string {
	b, err := json.Marshal(map[string]string{"gatewayToken": gw, "gatewayTokenExpiresAt": gwAt, "mcpToken": mcp, "mcpTokenExpiresAt": mcpAt})
	if err != nil {
		panic(err)
	}
	return string(b)
}

// credsProbe is a handler that hands the credentials of its welcome to the test.
func credsProbe() (funcHandler, <-chan *sdk.Credentials) {
	ch := make(chan *sdk.Credentials, 1)
	return funcHandler{start: func(_ context.Context, w sdk.Welcome) error {
		ch <- w.Credentials
		return nil
	}}, ch
}

// barrier sends a request the runner refuses at once and returns every line
// up to its answer. The runner reads lines in order, so everything sent before
// the barrier has been handled when it returns.
func (g *rig) barrier() []string {
	g.t.Helper()
	g.request("deliver", `{"kind":"chat","turnId":"`+turnB+`","message":`+chatPayloadJSON+`}`)
	return g.until(`"code":1001`)
}

// recv waits for one value, so a runner that never gets there fails the test
// instead of hanging it.
func recv[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(10 * time.Second):
		t.Fatal("nothing arrived within 10 seconds")
	}
	var zero T
	return zero
}

func tokens(c *sdk.Credentials) string {
	return c.GatewayToken().Reveal() + " " + c.MCPToken().Reveal()
}

func TestHelloOffersProtocol11Then10(t *testing.T) {
	g := newRig(t, echoOpts(), funcHandler{})
	if l := g.line(); !strings.Contains(l, `"protocolVersions":["1.1","1.0"]`) {
		t.Fatalf("hello = %s, want protocolVersions [1.1 1.0] by default", l)
	}
}

func TestWelcomeTokensAreTheFirstCredentials(t *testing.T) {
	h, creds := credsProbe()
	g := newRig(t, echoOpts(), h)
	g.ready()
	c := recv(t, creds)
	if got := tokens(c); got != "model-secret mcp-secret" {
		t.Fatalf("tokens = %q, want the welcome's", got)
	}
}

func TestStdioMCPTokenComesFromTheEnvironment(t *testing.T) {
	h, creds := credsProbe()
	g := newRig(t, echoOpts(), h)
	stdio := strings.Replace(welcomeJSON,
		`"mcp":{"transport":"streamable-http","url":"http://x/mcp","token":"mcp-secret","tokenExpiresAt":"2026-11-20T19:00:00Z"}`,
		`"mcp":{"transport":"stdio","command":"agentctl","args":["mcp"],"env":{"AGENTSONE_TOKEN":"stdio-secret-token-1"}}`, 1)
	g.handshake(stdio)
	if got := recv(t, creds).MCPToken().Reveal(); got != "stdio-secret-token-1" {
		t.Fatalf("mcp token = %q, want AGENTSONE_TOKEN of mcp.env", got)
	}
}

// TestCredentialsUpdateSwapsBothTokens is rule K2 on the SDK: after the
// notification both tokens are the new ones, and the runner sends nothing back.
func TestCredentialsUpdateSwapsBothTokens(t *testing.T) {
	h, creds := credsProbe()
	g := newRig(t, echoOpts(), h)
	g.handshake(welcome11())
	c := recv(t, creds)
	g.send(credentialsLine(credentialsParams(freshGateway, farFuture, freshMCP, farFuture)))
	if lines := g.barrier(); len(lines) != 1 {
		t.Fatalf("the runner answered the notification: %s", joined(lines))
	}
	if got := tokens(c); got != freshGateway+" "+freshMCP {
		t.Fatalf("tokens = %q, want both fresh tokens", got)
	}
}

// TestCredentialsUpdateIsIgnoredOn10 is rule K3: a connection that negotiated
// 1.0 keeps the welcome's tokens and stays silent, as in 1.0.
func TestCredentialsUpdateIsIgnoredOn10(t *testing.T) {
	h, creds := credsProbe()
	opts := echoOpts()
	opts.Versions = []string{"1.0"}
	g := newRig(t, opts, h)
	g.ready()
	c := recv(t, creds)
	g.send(credentialsLine(credentialsParams(freshGateway, farFuture, freshMCP, farFuture)))
	if lines := g.barrier(); len(lines) != 1 {
		t.Fatalf("want no line for the notification on 1.0, got %s", joined(lines))
	}
	if got := tokens(c); got != "model-secret mcp-secret" {
		t.Fatalf("tokens = %q, want the welcome's on 1.0", got)
	}
}

// TestCredentialsUpdateRightAfterTheWelcome: an update the daemon sends
// straight after the welcome is not lost while the SDK reads the welcome.
func TestCredentialsUpdateRightAfterTheWelcome(t *testing.T) {
	for i := range 20 {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			h, creds := credsProbe()
			g := newRig(t, echoOpts(), h)
			g.line() // hello
			g.send(`{"jsonrpc":"2.0","id":1,"result":` + welcome11() + "}\n" +
				credentialsLine(credentialsParams(freshGateway, farFuture, freshMCP, farFuture)))
			c := recv(t, creds)
			g.barrier()
			if got := tokens(c); got != freshGateway+" "+freshMCP {
				t.Fatalf("tokens = %q, want the update sent right after the welcome", got)
			}
		})
	}
}

// TestInvalidCredentialsUpdateIsIgnored: a bad update keeps the current
// tokens and gives one warning log line that holds no token text.
func TestInvalidCredentialsUpdateIsIgnored(t *testing.T) {
	cases := map[string]string{
		"missing mcp token":      `{"gatewayToken":"` + freshGateway + `","gatewayTokenExpiresAt":"` + farFuture + `","mcpTokenExpiresAt":"` + farFuture + `"}`,
		"missing gateway expiry": `{"gatewayToken":"` + freshGateway + `","mcpToken":"` + freshMCP + `","mcpTokenExpiresAt":"` + farFuture + `"}`,
		"token in a time field":  credentialsParams(freshGateway, freshGateway, freshMCP, farFuture),
		"time with an offset":    credentialsParams(freshGateway, "2099-01-01T01:00:00+01:00", freshMCP, farFuture),
		"time without a zone":    credentialsParams(freshGateway, farFuture, freshMCP, "2099-01-01T00:00:00"),
		"gateway token expired":  credentialsParams(freshGateway, longAgo, freshMCP, farFuture),
		"mcp token expired":      credentialsParams(freshGateway, farFuture, freshMCP, longAgo),
		"token too short":        credentialsParams("short", farFuture, freshMCP, farFuture),
		"token with a space":     credentialsParams(freshGateway+" x", farFuture, freshMCP, farFuture),
		"token with a newline":   credentialsParams(freshGateway, farFuture, freshMCP+"\n", farFuture),
		"token not a string":     `{"gatewayToken":12345678901234567890,"gatewayTokenExpiresAt":"` + farFuture + `","mcpToken":"` + freshMCP + `","mcpTokenExpiresAt":"` + farFuture + `"}`,
		"token as field name":    `{"gatewayToken":"` + freshGateway + `","gatewayTokenExpiresAt":"` + farFuture + `","mcpToken":"` + freshMCP + `","mcpTokenExpiresAt":"` + farFuture + `","` + freshMCP + `x":1}`,
		"params not an object":   `"` + freshGateway + `"`,
		"params null":            `null`,
	}
	for name, params := range cases {
		t.Run(name, func(t *testing.T) {
			h, creds := credsProbe()
			g := newRig(t, echoOpts(), h)
			g.handshake(welcome11())
			c := recv(t, creds)
			g.send(credentialsLine(params))
			lines := g.barrier()
			if len(lines) != 2 || !strings.HasPrefix(lines[0], `{"jsonrpc":"2.0","method":"log","params":{"level":"warn","message":"credentials.update ignored: `) {
				t.Fatalf("want one warning log line and the barrier answer, got\n%s", joined(lines))
			}
			for _, secret := range []string{freshGateway, freshMCP, "fresh-", "model-secret", "mcp-secret"} {
				if strings.Contains(joined(lines), secret) {
					t.Fatalf("the log line holds token text %q: %s", secret, lines[0])
				}
			}
			if got := tokens(c); got != "model-secret mcp-secret" {
				t.Fatalf("tokens = %q, want the welcome's after a bad update", got)
			}
		})
	}
}

func TestMissingParamsAreIgnored(t *testing.T) {
	h, creds := credsProbe()
	g := newRig(t, echoOpts(), h)
	g.handshake(welcome11())
	c := recv(t, creds)
	g.send(`{"jsonrpc":"2.0","method":"credentials.update"}`)
	if lines := g.barrier(); len(lines) != 2 || !strings.Contains(lines[0], "credentials.update ignored") {
		t.Fatalf("want a warning, got %s", joined(lines))
	}
	if got := tokens(c); got != "model-secret mcp-secret" {
		t.Fatalf("tokens = %q, want the welcome's", got)
	}
}

// TestCredentialsUpdateAsARequestIsRefused: credentials.update is a
// notification; sent with an id it is the wrong kind and changes nothing.
func TestCredentialsUpdateAsARequestIsRefused(t *testing.T) {
	h, creds := credsProbe()
	g := newRig(t, echoOpts(), h)
	g.handshake(welcome11())
	c := recv(t, creds)
	g.request("credentials.update", credentialsParams(freshGateway, farFuture, freshMCP, farFuture))
	if l := g.line(); !strings.Contains(l, `"id":1,"error":{"code":-32601`) {
		t.Fatalf("want -32601, got %s", l)
	}
	if got := tokens(c); got != "model-secret mcp-secret" {
		t.Fatalf("tokens = %q, want the welcome's", got)
	}
}

// TestCredentialsUpdateMidTurn: the turn that is running keeps running, and
// its next read of the tokens sees the new ones (K2).
func TestCredentialsUpdateMidTurn(t *testing.T) {
	seen := make(chan string, 2)
	var c *sdk.Credentials
	h := funcHandler{
		start: func(_ context.Context, w sdk.Welcome) error { c = w.Credentials; return nil },
		wake: func(ctx context.Context, tn *sdk.Turn, _ sdk.Wake) error {
			if err := tn.ProfileUpdate("Goal: test. Now: testing."); err != nil {
				return err
			}
			seen <- tokens(c)
			select {
			case <-tn.Delivered():
			case <-ctx.Done():
				return ctx.Err()
			}
			seen <- tokens(c)
			return tn.FinishOutput("done")
		},
	}
	g := newRig(t, echoOpts(), h)
	g.handshake(welcome11())
	g.wakeChat(turnA)
	if before := recv(t, seen); before != "model-secret mcp-secret" {
		t.Fatalf("tokens before the update = %q", before)
	}
	g.send(credentialsLine(credentialsParams(freshGateway, farFuture, freshMCP, farFuture)))
	g.request("deliver", `{"kind":"chat","turnId":"`+turnA+`","message":`+chatPayloadJSON+`}`)
	if after := recv(t, seen); after != freshGateway+" "+freshMCP {
		t.Fatalf("tokens after the update = %q, want the fresh ones", after)
	}
	lines := joined(g.until(`"state":"idle","turnId":"` + turnA))
	if strings.Contains(lines, "Cancelled") || strings.Contains(lines, `"method":"error"`) {
		t.Fatalf("the update interrupted the turn:\n%s", lines)
	}
}

// TestCredentialsAreSafeForConcurrentUse reads the tokens from many
// goroutines while updates arrive; run with -race.
func TestCredentialsAreSafeForConcurrentUse(t *testing.T) {
	h, ch := credsProbe()
	g := newRig(t, echoOpts(), h)
	g.handshake(welcome11())
	c := recv(t, ch)
	stop := make(chan struct{})
	var wg sync.WaitGroup
	var bad sync.Map
	for range 8 {
		wg.Go(func() {
			for {
				select {
				case <-stop:
					return
				default:
				}
				if tok := c.GatewayToken().Reveal(); tok != "model-secret" && !strings.HasPrefix(tok, "rotated-gateway-") {
					bad.Store(tok, true)
				}
				_ = c.MCPToken().Reveal()
			}
		})
	}
	const n = 50
	for i := range n {
		g.send(credentialsLine(credentialsParams(fmt.Sprintf("rotated-gateway-%04d", i), farFuture, fmt.Sprintf("rotated-mcp-token-%04d", i), farFuture)))
	}
	g.barrier()
	close(stop)
	wg.Wait()
	bad.Range(func(k, _ any) bool { t.Errorf("read a token that was never sent: %q", k); return true })
	if got := tokens(c); got != fmt.Sprintf("rotated-gateway-%04d rotated-mcp-token-%04d", n-1, n-1) {
		t.Fatalf("tokens = %q, want the last update", got)
	}
}

// TestCredentialsNeverPrint: the credentials and a welcome that holds them
// print and encode without any token.
func TestCredentialsNeverPrint(t *testing.T) {
	var w sdk.Welcome
	h := funcHandler{start: func(_ context.Context, got sdk.Welcome) error { w = got; return nil }}
	g := newRig(t, echoOpts(), h)
	g.handshake(welcome11())
	g.send(credentialsLine(credentialsParams(freshGateway, farFuture, freshMCP, farFuture)))
	g.barrier()
	b, err := json.Marshal(w)
	if err != nil {
		t.Fatal(err)
	}
	out := strings.Join([]string{
		fmt.Sprint(w.Credentials), fmt.Sprintf("%v %+v %#v %s %q", w.Credentials, w.Credentials, w.Credentials, w.Credentials, w.Credentials),
		fmt.Sprintf("%v %+v %#v", w, w, w), string(b),
		fmt.Sprint(w.Credentials.GatewayToken(), w.Credentials.MCPToken()),
	}, "\n")
	for _, secret := range []string{freshGateway, freshMCP, "model-secret", "mcp-secret"} {
		if strings.Contains(out, secret) {
			t.Fatalf("token %q printed:\n%s", secret, out)
		}
	}
}
