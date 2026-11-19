// SPDX-License-Identifier: MIT

package conformance

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Rule is one conformance test. ID is the rule id in PROTOCOL.md.
type Rule struct {
	ID    string
	Title string
	Run   func(t T, s Subject)
}

// Rules lists the runner-side rules of PROTOCOL.md that the suite checks, in
// document order. The daemon-side rules H3 to H6, B2 and P1 are checked
// against the daemon.
func Rules() []Rule {
	return []Rule{
		{"F1", "every line is one schema-valid JSON object ended by one newline", ruleF1},
		{"F2", "no line is longer than maxLineBytes", ruleF2},
		{"F3", "a line longer than maxLineBytes is answered with -32001 and the connection stays open", ruleF3},
		{"F4", "a line that is not JSON gets -32700 and a JSON array gets -32600, both with id null", ruleF4},
		{"F5", "request ids start at 1 and rise by 1", ruleF5},
		{"F6", "every request gets exactly one response and notifications get none", ruleF6},
		{"F7", "an unknown method or one sent in the wrong direction gets -32601", ruleF7},
		{"F8", "params that fail the schema get -32602", ruleF8},
		{"H1", "the first line is hello", ruleH1},
		{"H2", "hello lists protocol versions highest first without repeats", ruleH2},
		{"H7", "right after the welcome the runner sends status idle", ruleH7},
		{"T1", "wake is answered with an empty result before any other message about the turn", ruleT1},
		{"T2", "after the acknowledgement the next message about the turn is status working", ruleT2},
		{"T3", "every turn has a profile.update before the status that ends it", ruleT3},
		{"T4", "every turn ends with status idle", ruleT4},
		{"T5", "output chunks are numbered from 0 and the last one is final", ruleT5},
		{"T6", "output, usage, profile.update, bus.send and task.done carry the running turn id", ruleT6},
		{"T7", "one turn at a time: a wake during a turn is acknowledged and started after it", ruleT7},
		{"T8", "deliver is answered with an empty result, and 1001 for a turn that is not running", ruleT8},
		{"C1", "cancel is answered with an empty result, also for a turn that ended", ruleC1},
		{"C2", "after cancel the runner sends status idle for the turn", ruleC2},
		{"S1", "sleep is answered with a bounded summary followed by status sleeping", ruleS1},
		{"S2", "a session summary in the welcome starts the session", ruleS2},
		{"X1", "shutdown is answered, then the runner closes the socket and exits 0", ruleX1},
		{"X2", "when the socket closes without shutdown the runner exits non-zero", ruleX2},
		{"B1", "the runner sends heartbeats, also during turns", ruleB1},
		{"R1", "a budget refusal sends error 1010 and status idle with pausedTurnId", ruleR1},
		{"R2", "a budget.restored wake resumes the paused turn", ruleR2},
		{"R3", "a resume wake the runner cannot resume gets error 1011", ruleR3},
	}
}

func ready(t T, s Subject, mutate func(*WelcomeSpec)) *H {
	t.Helper()
	return Ready(t, s, mutate)
}

func plain(t T, s Subject) string {
	t.Helper()
	if s.Prompts.Plain == "" {
		t.Fatalf("Subject.Prompts.Plain is empty; the suite needs a text that gives a short turn")
	}
	return s.Prompts.Plain
}

func hold(t T, s Subject) string {
	t.Helper()
	if s.Prompts.Hold == "" {
		t.Fatalf("Subject.Prompts.Hold is empty; the suite needs a text that keeps a turn open")
	}
	return s.Prompts.Hold
}

// holdTurn starts a turn that stays open and waits until it is working.
func holdTurn(h *H) string {
	h.T.Helper()
	turnID, id := h.WakeChat(hold(h.T, h.Sub))
	msgs := h.Until(func(m Msg) bool { return m.Method == "status" && m.Field("state") == "working" && m.Field("turnId") == turnID })
	if first := msgs[0]; !first.IsResponse() || *first.ID != id {
		h.T.Fatalf("T1: the first line after wake must be its response, got %s", first)
	}
	return turnID
}

func requireEmptyResult(t T, m Msg, what string) {
	t.Helper()
	if m.Error != nil || strings.TrimSpace(string(m.Result)) != "{}" {
		t.Fatalf("%s: want an empty result, got %s", what, m)
	}
}

func hasMethod(msgs []Msg, method string) bool {
	for _, m := range msgs {
		if m.Method == method {
			return true
		}
	}
	return false
}

func ruleF1(t T, s Subject) {
	h := ready(t, s, nil)
	// Every line is checked against the schema as it arrives (Msg check); run
	// a turn, a task, a deliver, a cancel and a sleep so most message types appear.
	h.RunTurn(plain(t, s))
	task, _ := h.WakeTask("Check things", "Look at the machine.")
	h.Until(TurnEnded(task))
	turnID := holdTurn(h)
	h.AwaitResponse(h.Deliver(turnID, "more"))
	h.AwaitResponse(h.Cancel(turnID))
	h.Until(TurnEnded(turnID))
	h.AwaitResponse(h.Request("sleep", map[string]any{"reason": "idle", "maxSummaryChars": 256}))
	if len(h.Log) < 10 {
		t.Fatalf("F1: only %d lines were checked", len(h.Log))
	}
}

func ruleF2(t T, s Subject) {
	if s.Prompts.Long == nil {
		t.Skipf("Subject.Prompts.Long is not set")
	}
	h := ready(t, s, func(w *WelcomeSpec) { w.MaxLineBytes = 65536 })
	h.RunTurn(s.Prompts.Long(200_000))
	for _, m := range h.Log {
		if len(m.Raw) > 65536 {
			t.Fatalf("F2: a line is %d bytes, over maxLineBytes 65536", len(m.Raw))
		}
	}
}

func ruleF3(t T, s Subject) {
	h := ready(t, s, func(w *WelcomeSpec) { w.MaxLineBytes = 65536 })
	huge := `{"jsonrpc":"2.0","id":1,"method":"nope","params":{"p":"` + strings.Repeat("A", 70000) + `"}}` + "\n"
	h.SendRaw([]byte(huge))
	h.ExpectError(nil, -32001, "F3")
	id := h.Request("nope", map[string]any{})
	h.ExpectError(&id, -32601, "F3: the connection must stay open after an oversize line")
}

func ruleF4(t T, s Subject) {
	h := ready(t, s, nil)
	h.SendRaw([]byte("this is not json\n"))
	h.ExpectError(nil, -32700, "F4 not JSON")
	h.SendRaw([]byte("[]\n"))
	h.ExpectError(nil, -32600, "F4 empty array")
	h.SendRaw([]byte(`[{"jsonrpc":"2.0","id":9,"method":"nope"}]` + "\n"))
	h.ExpectError(nil, -32600, "F4 batch")
}

func ruleF5(t T, s Subject) {
	h := ready(t, s, nil)
	for range 3 {
		task, _ := h.WakeTask("Do a thing", "Do the thing.")
		h.Until(TurnEnded(task))
	}
	requests := 0
	for _, m := range h.Log {
		if m.IsRequest() {
			requests++
		}
	}
	// The ids are checked as each line arrives; hello is id 1, so a runner that
	// reports finished tasks has sent ids 2, 3 and 4 by now.
	if requests < 3 {
		t.Fatalf("F5: the runner sent %d requests; it must send task.done for each task, so ids can be checked", requests)
	}
}

func ruleF6(t T, s Subject) {
	h := ready(t, s, nil)
	h.Notify("nonsense", map[string]any{}) // a notification: no response
	id := h.Request("nope", map[string]any{})
	h.ExpectError(&id, -32601, "F6: the answer to the next request must be the first line, a notification gets none")
	turnID, ack := h.WakeChat(plain(t, s))
	msgs := h.Until(TurnEnded(turnID))
	answers := 0
	for _, m := range msgs {
		if m.IsResponse() && m.ID != nil && *m.ID == ack {
			answers++
		}
	}
	if answers != 1 {
		t.Fatalf("F6: wake got %d responses, want exactly 1", answers)
	}
}

func ruleF7(t T, s Subject) {
	h := ready(t, s, nil)
	for _, method := range []string{"nope", "hello", "status", "bus.send", "output", "heartbeat"} {
		id := h.Request(method, map[string]any{})
		h.ExpectError(&id, -32601, "F7 "+method)
	}
}

func ruleF8(t T, s Subject) {
	h := ready(t, s, nil)
	bad := []struct {
		method string
		params any
	}{
		{"wake", map[string]any{}},
		{"wake", map[string]any{"reason": "chat", "turnId": "nope", "payload": chatPayload("msg_"+ulid(1), "x")}},
		{"wake", map[string]any{"reason": "bogus", "turnId": "trn_" + ulid(1), "payload": map[string]any{}}},
		{"wake", map[string]any{"reason": "chat", "turnId": "trn_" + ulid(1), "payload": map[string]any{"text": "x"}}},
		{"deliver", map[string]any{}},
		{"deliver", map[string]any{"kind": "carrier-pigeon", "turnId": "trn_" + ulid(1), "message": map[string]any{}}},
		{"cancel", map[string]any{"turnId": "nope", "reason": "human"}},
		{"cancel", map[string]any{"turnId": "trn_" + ulid(1), "reason": "because"}},
		{"sleep", map[string]any{"reason": "idle", "maxSummaryChars": 1}},
		{"sleep", map[string]any{"reason": "nap", "maxSummaryChars": 256}},
		{"shutdown", map[string]any{}},
		{"shutdown", map[string]any{"reason": "stop", "graceMs": 1000, "extra": true}},
	}
	for _, b := range bad {
		id := h.Request(b.method, b.params)
		h.ExpectError(&id, -32602, fmt.Sprintf("F8 %s %v", b.method, b.params))
	}
}

func ruleH1(t T, s Subject) {
	h := Start(t, s, nil)
	h.Accept()
	select {
	case l := <-h.lines:
		if l.err != nil {
			t.Fatalf("H1: read hello: %v", l.err)
		}
		m := h.check(l.b)
		if !m.IsRequest() || m.Method != "hello" || *m.ID != 1 {
			t.Fatalf("H1: the first line must be hello with id 1, got %s", m)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("H1: no hello within 10 seconds of connecting")
	}
}

func ruleH2(t T, s Subject) {
	h := Start(t, s, nil)
	h.Accept()
	hello := h.RecvRaw()
	var p struct {
		ProtocolVersions []string `json:"protocolVersions"`
	}
	if err := json.Unmarshal(hello.Params, &p); err != nil || len(p.ProtocolVersions) == 0 {
		t.Fatalf("H2: hello has no protocolVersions: %s", hello)
	}
	seen := map[string]bool{}
	prev := [2]int{1 << 30, 1 << 30}
	for _, v := range p.ProtocolVersions {
		if seen[v] {
			t.Fatalf("H2: version %s is listed twice", v)
		}
		seen[v] = true
		cur, ok := parseVersion(v)
		if !ok {
			t.Fatalf("H2: %q is not a <major>.<minor> version", v)
		}
		if cur[0] > prev[0] || (cur[0] == prev[0] && cur[1] >= prev[1]) {
			t.Fatalf("H2: versions must be highest first: %v", p.ProtocolVersions)
		}
		prev = cur
	}
}

func parseVersion(v string) ([2]int, bool) {
	major, minor, ok := strings.Cut(v, ".")
	a, err1 := strconv.Atoi(major)
	b, err2 := strconv.Atoi(minor)
	return [2]int{a, b}, ok && err1 == nil && err2 == nil
}

func ruleH7(t T, s Subject) {
	ready(t, s, nil) // Handshake fails unless status idle follows the welcome
}

// turn runs one plain chat turn and returns its id, the ack id and all lines.
func turn(h *H) (string, int, []Msg) {
	h.T.Helper()
	turnID, ack := h.WakeChat(plain(h.T, h.Sub))
	return turnID, ack, h.Until(TurnEnded(turnID))
}

func ruleT1(t T, s Subject) {
	h := ready(t, s, nil)
	_, ack, msgs := turn(h)
	first := msgs[0]
	if !first.IsResponse() || first.ID == nil || *first.ID != ack {
		t.Fatalf("T1: the first line about the turn must be the wake response, got %s", first)
	}
	requireEmptyResult(t, first, "T1")
}

func ruleT2(t T, s Subject) {
	h := ready(t, s, nil)
	turnID, _, msgs := turn(h)
	if len(msgs) < 2 || msgs[1].Method != "status" || msgs[1].Field("state") != "working" || msgs[1].Field("turnId") != turnID {
		t.Fatalf("T2: after the acknowledgement the next line must be status working for %s, got %v", turnID, msgs[1:])
	}
}

func ruleT3(t T, s Subject) {
	h := ready(t, s, nil)
	turnID, _, msgs := turn(h)
	profile := -1
	for i, m := range msgs {
		if m.Method == "profile.update" && m.Field("turnId") == turnID {
			profile = i
			break
		}
	}
	if profile < 0 || profile >= len(msgs)-1 {
		t.Fatalf("T3: no profile.update with turnId %s before the ending status", turnID)
	}
}

func ruleT4(t T, s Subject) {
	h := ready(t, s, nil)
	turnID, _, msgs := turn(h)
	last := msgs[len(msgs)-1]
	if last.Field("state") != "idle" || last.Field("turnId") != turnID {
		t.Fatalf("T4: the turn must end with status idle and its turnId, got %s", last)
	}
}

func ruleT5(t T, s Subject) {
	h := ready(t, s, nil)
	turnID, _, msgs := turn(h)
	next, finalSeen := 0, false
	for _, m := range msgs {
		if m.Method != "output" || m.Field("turnId") != turnID {
			continue
		}
		if finalSeen {
			t.Fatalf("T5: output after the final chunk: %s", m)
		}
		if m.Field("chunk") != strconv.Itoa(next) {
			t.Fatalf("T5: chunk is %s, want %d", m.Field("chunk"), next)
		}
		next++
		finalSeen = m.Field("final") == "true"
	}
	if next == 0 || !finalSeen {
		t.Fatalf("T5: the turn needs output chunks and the last must have final true (chunks seen: %d)", next)
	}
}

func ruleT6(t T, s Subject) {
	h := ready(t, s, nil)
	turnID, _, chat := turn(h)
	task, _ := h.WakeTask("Write a note", "Write the weekly note.")
	tasks := h.Until(TurnEnded(task))
	check := func(want string, msgs []Msg) {
		for _, m := range msgs {
			switch m.Method {
			case "output", "usage", "profile.update", "bus.send", "task.done":
				if m.Field("turnId") != want {
					t.Fatalf("T6: %s carries turnId %q, want %q", m.Method, m.Field("turnId"), want)
				}
			}
		}
	}
	check(turnID, chat)
	check(task, tasks)
}

func ruleT7(t T, s Subject) {
	h := ready(t, s, nil)
	a := holdTurn(h)
	b, ack := h.WakeChat(plain(t, s))
	got := h.AwaitResponse(ack) // the first turn may still be sending its own lines
	requireEmptyResult(t, got, "T7")
	h.AwaitResponse(h.Cancel(a))
	msgs := h.Until(TurnEnded(a))
	for _, m := range msgs {
		if m.Method == "status" && m.Field("state") == "working" && m.Field("turnId") == b {
			t.Fatalf("T7: turn %s started before turn %s ended", b, a)
		}
	}
	h.Until(func(m Msg) bool { return m.Method == "status" && m.Field("state") == "working" && m.Field("turnId") == b })
	h.Until(TurnEnded(b))
}

func ruleT8(t T, s Subject) {
	h := ready(t, s, nil)
	a := holdTurn(h)
	requireEmptyResult(t, h.AwaitResponse(h.Deliver(a, "Are you there?")), "T8")
	other := "trn_" + ulid(30)
	m := h.AwaitResponse(h.Deliver(other, "hello?"))
	if m.Error == nil || m.Error.Code != 1001 {
		t.Fatalf("T8: deliver to a turn that is not running must get 1001, got %s", m)
	}
}

func ruleC1(t T, s Subject) {
	h := ready(t, s, nil)
	a := holdTurn(h)
	requireEmptyResult(t, h.AwaitResponse(h.Cancel(a)), "C1 running turn")
	h.Until(TurnEnded(a))
	requireEmptyResult(t, h.AwaitResponse(h.Cancel(a)), "C1 turn that ended")
}

func ruleC2(t T, s Subject) {
	h := ready(t, s, nil)
	a := holdTurn(h)
	h.AwaitResponse(h.Cancel(a))
	msgs := h.Until(TurnEnded(a)) // Until fails the test if no idle arrives within the guard
	last := msgs[len(msgs)-1]
	if last.Field("state") != "idle" || last.Field("turnId") != a {
		t.Fatalf("C2: cancel must end the turn with status idle for it, got %s", last)
	}
}

func ruleS1(t T, s Subject) {
	h := ready(t, s, nil)
	h.RunTurn(plain(t, s))
	m := h.AwaitResponse(h.Request("sleep", map[string]any{"reason": "idle", "maxSummaryChars": 256}))
	var res struct {
		Summary *string `json:"summary"`
	}
	if m.Error != nil || json.Unmarshal(m.Result, &res) != nil || res.Summary == nil {
		t.Fatalf("S1: sleep must be answered with a summary, got %s", m)
	}
	if n := utf8.RuneCountInString(*res.Summary); n > 256 {
		t.Fatalf("S1: summary is %d characters, over maxSummaryChars 256", n)
	}
	st := h.Next()
	if st.Method != "status" || st.Field("state") != "sleeping" {
		t.Fatalf("S1: the summary must be followed by status sleeping, got %s", st)
	}
}

func ruleS2(t T, s Subject) {
	if !s.SummaryInOutput {
		t.Skipf("the runner does not repeat the summary in its output; it proves S2 in its own tests")
	}
	const summary = "We talked about the weekly status note."
	h := ready(t, s, func(w *WelcomeSpec) { v := summary; w.Summary = &v })
	_, _, msgs := turn(h)
	for _, m := range msgs {
		if m.Method == "output" {
			if !strings.Contains(m.Field("text"), summary) {
				t.Fatalf("S2: the first output must hold the session summary, got %s", m)
			}
			return
		}
	}
	t.Fatalf("S2: the turn produced no output")
}

func ruleX1(t T, s Subject) {
	h := ready(t, s, nil)
	requireEmptyResult(t, h.AwaitResponse(h.Request("shutdown", map[string]any{"reason": "stop", "graceMs": 5000})), "X1")
	h.ReadUntilEOF()
	if code := h.WaitExit(5 * time.Second); code != 0 {
		t.Fatalf("X1: the runner exited with status %d after shutdown, want 0", code)
	}
}

func ruleX2(t T, s Subject) {
	h := ready(t, s, nil)
	h.CloseSocket()
	if code := h.WaitExit(5 * time.Second); code == 0 {
		t.Fatalf("X2: the runner exited 0 although the socket closed without shutdown, want non-zero")
	}
}

func ruleB1(t T, s Subject) {
	h := ready(t, s, func(w *WelcomeSpec) { w.HeartbeatIntervalMs = 1000; w.HeartbeatTimeoutMs = 3000 })
	holdTurn(h)
	// Two heartbeats while the turn is working. The exact interval is proven
	// where the clock is faked; here each gap only has to be far from a hang.
	beats, last := 0, time.Now()
	for beats < 2 {
		m := h.RecvRaw()
		if m.IsRequest() {
			h.Respond(*m.ID, map[string]any{})
		}
		if m.Is("heartbeat") {
			if gap := time.Since(last); gap > 5*time.Second {
				t.Fatalf("B1: %s between heartbeats with heartbeatIntervalMs 1000", gap)
			}
			last = time.Now()
			beats++
		}
	}
}

func budgetPrompt(t T, s Subject) string {
	t.Helper()
	if s.Prompts.Budget == "" {
		t.Skipf("the runner has no budget prompt; it must then declare resume false")
	}
	return s.Prompts.Budget
}

func pauseTurn(h *H) (string, []Msg) {
	h.T.Helper()
	turnID, _ := h.WakeChat(budgetPrompt(h.T, h.Sub))
	return turnID, h.Until(TurnEnded(turnID))
}

func ruleR1(t T, s Subject) {
	h := ready(t, s, nil)
	turnID, msgs := pauseTurn(h)
	errAt, idleAt := -1, len(msgs)-1
	for i, m := range msgs {
		if m.Method == "error" && m.Field("code") == "1010" && m.Field("turnId") == turnID {
			if m.Field("fatal") != "false" {
				t.Fatalf("R1: the budget error must have fatal false: %s", m)
			}
			errAt = i
		}
	}
	last := msgs[idleAt]
	if errAt < 0 || errAt > idleAt || last.Field("state") != "idle" || last.Field("pausedTurnId") != turnID {
		t.Fatalf("R1: want error 1010 then status idle with pausedTurnId %s, got %v", turnID, msgs)
	}
}

func ruleR2(t T, s Subject) {
	h := ready(t, s, nil)
	turnID, _ := pauseTurn(h)
	ack := h.WakeBudgetRestored(turnID)
	msgs := h.Until(TurnEnded(turnID))
	first := msgs[0]
	if !first.IsResponse() || *first.ID != ack || first.Error != nil {
		t.Fatalf("R2: the resume wake must be acknowledged first, got %s", first)
	}
	if len(msgs) < 3 || msgs[1].Field("state") != "working" || msgs[1].Field("turnId") != turnID {
		t.Fatalf("R2: the resumed turn must continue with status working for %s, got %v", turnID, msgs)
	}
	last := msgs[len(msgs)-1]
	if last.Field("state") != "idle" || last.Field("turnId") != turnID {
		t.Fatalf("R2: the resumed turn must end with status idle and its turnId, got %s", last)
	}
	if hasMethod(msgs, "error") {
		t.Fatalf("R2: the resumed turn must finish without a new error")
	}
}

func ruleR3(t T, s Subject) {
	h := ready(t, s, nil)
	unknown := "trn_" + ulid(31) // never paused: there is nothing to resume
	id := h.WakeBudgetRestored(unknown)
	h.ExpectError(&id, 1011, "R3")
}
