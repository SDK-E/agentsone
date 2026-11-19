// SPDX-License-Identifier: MIT

package conformance

import (
	"encoding/json"
	"slices"
)

// renewal is the params of a valid credentials.update, with the made-up
// tokens of transcript-1.1.ndjson and a far expiry so that a runner on a real
// clock accepts them.
func renewal() map[string]string {
	const far = "2099-01-01T00:00:00Z"
	return map[string]string{ //nolint:gosec // G101: the made-up tokens of transcript-1.1.ndjson
		"gatewayToken": "test-gateway-token-founder-0002", "gatewayTokenExpiresAt": far,
		"mcpToken": "test-mcp-token-founder-0002", "mcpTokenExpiresAt": far,
	}
}

func offers(hello Msg, version string) bool {
	var p struct {
		ProtocolVersions []string `json:"protocolVersions"`
	}
	return json.Unmarshal(hello.Params, &p) == nil && slices.Contains(p.ProtocolVersions, version)
}

// ruleK2 checks the wire side of K2 on a 1.1 connection: credentials.update
// gets no response and the running turn goes on. That the next model call and
// MCP request carry the new tokens depends on the runner's own fakes, so each
// runner proves it in its own tests.
func ruleK2(t T, s Subject) {
	h := Start(t, s, func(w *WelcomeSpec) { w.ProtocolVersion = "1.1" })
	h.Accept()
	hello := h.RecvRaw()
	if !hello.IsRequest() || hello.Method != "hello" {
		t.Fatalf("H1: the first line must be hello, got %s", hello)
	}
	if !offers(hello, "1.1") {
		t.Skipf("K2: the runner does not offer protocol 1.1")
	}
	h.Respond(*hello.ID, h.Welcome.result())
	if st := h.Next(); st.Method != "status" || st.Field("state") != "idle" {
		t.Fatalf("H7: right after the welcome the runner must send status idle, got %s", st)
	}
	turnID := holdTurn(h)
	h.Notify("credentials.update", renewal())
	ack := h.Deliver(turnID, "Still there?")
	msgs := h.Until(func(m Msg) bool { return m.IsResponse() && m.ID != nil && *m.ID == ack })
	for _, m := range msgs[:len(msgs)-1] {
		if m.IsResponse() {
			t.Fatalf("K2: credentials.update is a notification and gets no response, got %s", m)
		}
		if m.Method == "status" {
			t.Fatalf("K2: credentials.update must not stop the running turn, got %s", m)
		}
	}
	requireEmptyResult(t, msgs[len(msgs)-1], "K2: deliver into the turn after credentials.update")
	h.AwaitResponse(h.Cancel(turnID))
	h.Until(TurnEnded(turnID))
}

// ruleK3 checks that a connection that negotiated 1.0 behaves as in 1.0: a
// stray credentials.update changes nothing on the wire.
func ruleK3(t T, s Subject) {
	h := ready(t, s, nil) // DefaultWelcome chooses 1.0
	h.Notify("credentials.update", renewal())
	turnID, ack := h.WakeChat(plain(t, s))
	if first := h.Next(); !first.IsResponse() || first.ID == nil || *first.ID != ack {
		t.Fatalf("K3: on 1.0 credentials.update must get nothing; the next line must answer wake, got %s", first)
	}
	h.Until(TurnEnded(turnID))
}
