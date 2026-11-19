// SPDX-License-Identifier: MIT

package openai_test

import (
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/runners/conformance"
)

// Renewed tokens of the credentials.update the tests send. Made-up values.
const (
	renewedGateway = "gw-token-renewed-0002"
	renewedMCP     = "test-mcp-token-founder-0002"
	farFuture      = "2099-01-01T00:00:00Z"
)

func renew(h *conformance.H) {
	h.T.Helper()
	h.Notify("credentials.update", map[string]string{
		"gatewayToken": renewedGateway, "gatewayTokenExpiresAt": farFuture,
		"mcpToken": renewedMCP, "mcpTokenExpiresAt": farFuture,
	})
	// A request the runner refuses at once: once it is answered, the
	// notification before it has been handled.
	id := h.Request("deliver", map[string]any{"kind": "chat", "turnId": h.NewTurnID(),
		"message": map[string]any{"id": "msg_01JD8X3Q5Y6Z7A8B9C0D1E2FZZ", "chatId": "cht_01JD8X3Q5Y6Z7A8B9C0D1E2F40",
			"from": "human:alice", "text": "x", "sentAt": "2026-11-19T19:00:01Z"}})
	if m := h.AwaitResponse(id); !strings.Contains(m.String(), `"code":1001`) {
		h.T.Fatalf("want 1001 for the barrier deliver, got %s", m)
	}
}

func v11(w *conformance.WelcomeSpec) { w.ProtocolVersion = "1.1" }

// TestTokenRenewalMidTurn is rule K2 on the openai runner: a model call in
// flight finishes with the old token, the next MCP request and the next model
// call carry the new ones, and the turn runs on without a restart.
func TestTokenRenewalMidTurn(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var step atomic.Int64
	respond := func(gwRequest) reply {
		if step.Add(1) == 1 {
			close(started)
			<-release // in flight while the tokens change
			return reply{Calls: []fakeCall{{ID: "c1", Name: "add", Args: `{"a":2,"b":3}`}}}
		}
		return reply{Text: []string{"the sum is 5"}}
	}
	r := &rig{mcp: newMCP(t, mcpToken, renewedMCP)}
	r.gw = newGateway(t, respond)
	h := conformance.Ready(t, r.subject(), v11)
	oldGateway := h.Welcome.ModelToken
	turnID, _ := h.WakeChat("add 2 and 3")
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("the first model call did not arrive")
	}
	renew(h)
	close(release)
	msgs := h.Until(conformance.TurnEnded(turnID))
	if got := outputText(msgs); !strings.Contains(got, "the sum is 5") {
		t.Fatalf("the turn did not finish its work: %q", got)
	}
	for _, m := range msgs {
		if m.Method == "error" || (m.Method == "status" && m.Field("detail") != "") {
			t.Fatalf("the renewal interrupted the turn: %s", m)
		}
	}
	reqs := r.gw.requests()
	if len(reqs) != 2 || reqs[0].Auth != "Bearer "+oldGateway || reqs[1].Auth != "Bearer "+renewedGateway {
		t.Fatalf("gateway tokens = %v, want the old one in flight, then the renewed one", auths(reqs))
	}
	seen := r.mcp.seenTokens()
	if len(seen) < 2 || seen[0] != mcpToken || seen[len(seen)-1] != renewedMCP {
		t.Fatalf("mcp tokens = %v, want the welcome's first and the renewed one for the tool call", seen)
	}
	if got := r.mcp.called(); len(got) != 1 || got[0] != "add" {
		t.Fatalf("tools called = %v, want add", got)
	}
	_, next := h.RunTurn("again")
	if reqs := r.gw.requests(); reqs[len(reqs)-1].Auth != "Bearer "+renewedGateway {
		t.Fatalf("the next turn used %q, want the renewed token", reqs[len(reqs)-1].Auth)
	}
	assertNoTokens(t, append(h.Log, next...), oldGateway, renewedGateway, mcpToken, renewedMCP)
}

// TestRenewedTokenServesSleep: the compaction call outside a turn uses the
// latest token too.
func TestRenewedTokenServesSleep(t *testing.T) {
	r := newRig(t, nil)
	h := conformance.Ready(t, r.subject(), v11)
	h.RunTurn("Hello")
	renew(h)
	id := h.Request("sleep", map[string]any{"reason": "idle", "maxSummaryChars": 4096})
	h.AwaitResponse(id)
	reqs := r.gw.requests()
	if last := reqs[len(reqs)-1]; last.Auth != "Bearer "+renewedGateway {
		t.Fatalf("sleep used %q, want the renewed token", last.Auth)
	}
}

// TestTokensStayOutOfErrors: a gateway that echoes the bearer token in a
// refusal does not get it into the runner's error line.
func TestTokensStayOutOfErrors(t *testing.T) {
	r := newRig(t, func(gwRequest) reply { return reply{Status: http.StatusUnauthorized, EchoAuth: true} })
	h := conformance.Ready(t, r.subject(), v11)
	renew(h)
	_, msgs := h.RunTurn("Hello")
	var sawError bool
	for _, m := range msgs {
		sawError = sawError || m.Method == "error"
	}
	if !sawError {
		t.Fatal("want an error line for the refused call")
	}
	assertNoTokens(t, h.Log, h.Welcome.ModelToken, renewedGateway, mcpToken, renewedMCP)
}

func auths(reqs []gwRequest) []string {
	out := make([]string, 0, len(reqs))
	for _, r := range reqs {
		out = append(out, r.Auth)
	}
	return out
}

// assertNoTokens scans every line the runner sent for token text.
func assertNoTokens(t *testing.T, msgs []conformance.Msg, secrets ...string) {
	t.Helper()
	for _, m := range msgs {
		for _, s := range secrets {
			if strings.Contains(string(m.Raw), s) {
				t.Fatalf("a token reached the wire: %s", m)
			}
		}
	}
}
