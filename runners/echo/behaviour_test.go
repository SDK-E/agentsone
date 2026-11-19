// SPDX-License-Identifier: MIT

package echo_test

import (
	"strings"
	"testing"

	"github.com/SDK-E/agentsone/runners/conformance"
	"github.com/SDK-E/agentsone/runners/echo"
)

func texts(msgs []conformance.Msg) string {
	var b strings.Builder
	for _, m := range msgs {
		if m.Method == "output" {
			b.WriteString(m.Field("text"))
		}
	}
	return b.String()
}

// TestChatTurnIsDeterministic compares every line of one chat turn with the
// golden lines (TESTING B4c: the echo runner never changes its output).
func TestChatTurnIsDeterministic(t *testing.T) {
	h := conformance.Ready(t, subject(), nil)
	turnID, msgs := h.RunTurn("Hello echo")
	want := []string{
		`{"jsonrpc":"2.0","id":1,"result":{}}`,
		`{"jsonrpc":"2.0","method":"status","params":{"state":"working","turnId":"` + turnID + `"}}`,
		`{"jsonrpc":"2.0","method":"profile.update","params":{"turnId":"` + turnID + `","line":"Goal: echo what I am told. Now: echoing Hello echo"}}`,
		`{"jsonrpc":"2.0","method":"usage","params":{"turnId":"` + turnID + `","model":"echo","weight":"quick","tokensIn":0,"tokensOut":0,"costMicroUsd":0}}`,
		`{"jsonrpc":"2.0","method":"output","params":{"turnId":"` + turnID + `","chatId":"cht_01JD8X3Q5Y6Z7A8B9C0D1E2F01","chunk":0,"text":"echo: Hello echo","final":true}}`,
		`{"jsonrpc":"2.0","method":"status","params":{"state":"idle","turnId":"` + turnID + `"}}`,
	}
	if len(msgs) != len(want) {
		t.Fatalf("got %d lines, want %d: %v", len(msgs), len(want), msgs)
	}
	for i, m := range msgs {
		if m.String() != want[i] {
			t.Errorf("line %d:\n got %s\nwant %s", i+1, m, want[i])
		}
	}
}

func TestEveryWakeReasonIsEchoed(t *testing.T) {
	h := conformance.Ready(t, subject(), nil)
	cases := []struct {
		name, params, want string
	}{
		{"timer", `{"reason":"timer","turnId":"trn_01JD8X3Q5Y6Z7A8B9C0D1E2F3J","payload":{"scheduleId":"sch_01JD8X3Q5Y6Z7A8B9C0D1E2F70","name":"nightly-review","due":"2026-11-19T19:10:00Z","firedAt":"2026-11-19T19:10:00.012Z"}}`, "echo: timer nightly-review"},
		{"event", `{"reason":"event","turnId":"trn_01JD8X3Q5Y6Z7A8B9C0D1E2F3K","payload":{"specversion":"1.0","id":"evt_1","source":"/s","type":"agentsone.test.ping","time":"2026-11-19T19:05:00Z","datacontenttype":"application/json","data":{},"sequence":"1","actor":"host:agentsone","chartscope":"agent:founder"}}`, "echo: event agentsone.test.ping"},
		{"message", `{"reason":"message","turnId":"trn_01JD8X3Q5Y6Z7A8B9C0D1E2F3M","payload":{"id":"msg_01JD8X3Q5Y6Z7A8B9C0D1E2F44","from":"agent:watcher","to":"founder","subject":"s","body":"Noted.","sentAt":"2026-11-19T19:11:00Z"}}`, "echo: Noted."},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			turnID := strings.Split(strings.Split(c.params, `"turnId":"`)[1], `"`)[0]
			var params map[string]any
			if err := jsonUnmarshal(c.params, &params); err != nil {
				t.Fatal(err)
			}
			h.Request("wake", params)
			msgs := h.Until(conformance.TurnEnded(turnID))
			if got := texts(msgs); got != c.want {
				t.Fatalf("output = %q, want %q", got, c.want)
			}
		})
	}
}

func TestTaskIsEchoedAndReportedDone(t *testing.T) {
	h := conformance.Ready(t, subject(), nil)
	turnID, _ := h.WakeTask("Write the note", "Weekly.")
	msgs := h.Until(conformance.TurnEnded(turnID))
	if got := texts(msgs); got != "echo: task Write the note: Weekly." {
		t.Fatalf("output = %q", got)
	}
	var done conformance.Msg
	for _, m := range msgs {
		if m.Method == "task.done" {
			done = m
		}
	}
	if done.Field("outcome") != "done" || done.Field("summary") != "Echoed task: Write the note" {
		t.Fatalf("task.done = %s", done)
	}
}

func TestDeliveriesAreEchoedWhileHolding(t *testing.T) {
	h := conformance.Ready(t, subject(), nil)
	turnID, _ := h.WakeChat(echo.CmdHold)
	h.Until(func(m conformance.Msg) bool { return m.Method == "output" })
	h.Deliver(turnID, "first")
	h.Deliver(turnID, "second")
	var seen string
	h.Until(func(m conformance.Msg) bool {
		if m.Method == "output" {
			seen += m.Field("text")
		}
		return strings.Contains(seen, "echo: first") && strings.Contains(seen, "echo: second")
	})
	h.AwaitResponse(h.Cancel(turnID))
	h.Until(conformance.TurnEnded(turnID))
}

func TestSleepSummaryStartsFromTheWelcomeSummary(t *testing.T) {
	summary := "We talked about the weekly note."
	h := conformance.Ready(t, subject(), func(w *conformance.WelcomeSpec) { w.Summary = &summary })
	h.RunTurn("Hello echo")
	res := h.AwaitResponse(h.Request("sleep", map[string]any{"reason": "idle", "maxSummaryChars": 256}))
	got := res.Field("summary")
	if got == "" {
		var r struct{ Summary string }
		if err := jsonUnmarshal(string(res.Result), &r); err != nil {
			t.Fatal(err)
		}
		got = r.Summary
	}
	want := summary + " Echoed 1 messages. Last: Hello echo"
	if got != want {
		t.Fatalf("summary = %q, want %q", got, want)
	}
}

func TestSleepSummaryIsCutToTheLimit(t *testing.T) {
	long := strings.Repeat("ü", 400)
	h := conformance.Ready(t, subject(), nil)
	h.RunTurn(long)
	res := h.AwaitResponse(h.Request("sleep", map[string]any{"reason": "idle", "maxSummaryChars": 256}))
	var r struct{ Summary string }
	if err := jsonUnmarshal(string(res.Result), &r); err != nil {
		t.Fatal(err)
	}
	if n := len([]rune(r.Summary)); n != 256 {
		t.Fatalf("summary has %d characters, want 256", n)
	}
}

func TestLongCommand(t *testing.T) {
	h := conformance.Ready(t, subject(), nil)
	_, msgs := h.RunTurn(echo.CmdLong + " 12")
	if got := texts(msgs); got != "echo echo ec" {
		t.Fatalf("output = %q", got)
	}
	_, msgs = h.RunTurn(echo.CmdLong + " nope")
	if got := texts(msgs); got != "echo: usage /long N" {
		t.Fatalf("output = %q", got)
	}
}

func TestResumeFinishesThePausedTurn(t *testing.T) {
	h := conformance.Ready(t, subject(), nil)
	turnID, _ := h.WakeChat(echo.CmdBudget)
	h.Until(conformance.TurnEnded(turnID))
	h.WakeBudgetRestored(turnID)
	msgs := h.Until(conformance.TurnEnded(turnID))
	if got := texts(msgs); got != "echo: resumed" {
		t.Fatalf("output = %q", got)
	}
}
