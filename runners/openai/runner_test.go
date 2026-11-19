// SPDX-License-Identifier: MIT

package openai_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/SDK-E/agentsone/runners/conformance"
	"github.com/SDK-E/agentsone/runners/openai"
	"github.com/SDK-E/agentsone/runners/sdk"
)

const (
	promptHold   = "HOLD"
	promptBudget = "BUDGET"
	mcpToken     = "test-mcp-token-founder-0001" //nolint:gosec // G101: the made-up token of the PROTOCOL.md transcript
)

var longRe = regexp.MustCompile(`LONG (\d+)`)

// scripted is the default fake model: it echoes, holds, or runs out of budget
// once per token, depending on the prompt.
func scripted(g **fakeGateway) func(gwRequest) reply {
	return func(r gwRequest) reply {
		last := r.last().Content
		switch {
		case strings.HasPrefix(last, "Summarise this session"):
			return reply{Text: []string{"Summary: we talked."}}
		case strings.Contains(last, promptHold):
			return reply{Hold: true}
		case strings.Contains(last, promptBudget) && (*g).firstTime(r.Auth):
			return reply{Status: http.StatusPaymentRequired}
		case longRe.MatchString(last):
			n, _ := strconv.Atoi(longRe.FindStringSubmatch(last)[1])
			return reply{Text: []string{strings.Repeat("x", n)}}
		}
		return reply{Text: []string{"reply to: ", last}}
	}
}

// rig is the fakes and the subject that points the runner at them.
type rig struct {
	gw     *fakeGateway
	mcp    *fakeMCP
	tokens atomic.Int64
}

func newRig(t *testing.T, respond func(gwRequest) reply) *rig {
	t.Helper()
	r := &rig{mcp: newMCP(t, mcpToken)}
	if respond == nil {
		respond = scripted(&r.gw)
	}
	r.gw = newGateway(t, respond)
	return r
}

func (r *rig) subject() conformance.Subject {
	return conformance.Subject{
		Name: "openai",
		Start: conformance.InProcess(func(ctx context.Context, socket string) error {
			opts := openai.Options()
			opts.Socket = socket
			runner := openai.New(openai.Config{})
			defer func() { _ = runner.Close() }()
			return sdk.Run(ctx, opts, runner)
		}),
		Prompts: conformance.Prompts{
			Plain:  "Hello",
			Hold:   promptHold,
			Budget: promptBudget,
			Long:   func(n int) string { return fmt.Sprintf("LONG %d", n) },
		},
		SummaryInOutput: false,
		Welcome: func(w *conformance.WelcomeSpec) {
			w.ModelBaseURL = r.gw.URL()
			w.ModelToken = fmt.Sprintf("gw-token-%d", r.tokens.Add(1))
			w.MCPURL = r.mcp.srv.URL
		},
	}
}

// TestConformance runs every rule of PROTOCOL.md against the runner with a
// fake gateway and a fake MCP server (TESTING.md B4c).
func TestConformance(t *testing.T) {
	r := newRig(t, nil)
	conformance.Run(t, r.subject())
}

func outputText(msgs []conformance.Msg) string {
	var b strings.Builder
	for _, m := range msgs {
		if m.Method == "output" {
			b.WriteString(m.Field("text"))
		}
	}
	return b.String()
}

func methods(msgs []conformance.Msg, method string) []conformance.Msg {
	var out []conformance.Msg
	for _, m := range msgs {
		if m.Method == method {
			out = append(out, m)
		}
	}
	return out
}

// TestPlainTurn checks the gateway call of an ordinary turn: the welcome's
// URL, token, model name and weight, the streamed output, the usage report and
// the profile line (PROTOCOL.md T3, T5, T6).
func TestPlainTurn(t *testing.T) {
	r := newRig(t, nil)
	h := conformance.Ready(t, r.subject(), nil)
	_, msgs := h.RunTurn("Hello")
	if got := outputText(msgs); got != "reply to: human:alice says: Hello" {
		t.Fatalf("output %q", got)
	}
	req := r.gw.requests()[0]
	if req.Auth != "Bearer gw-token-1" || req.Model != "autoagents/one" || req.Weight != "normal" || !req.Stream || !req.IncludeUsage {
		t.Fatalf("gateway request wrong: %+v", req)
	}
	if req.Messages[0].Role != "system" || !strings.Contains(req.Messages[0].Content, "Hire agents") {
		t.Fatalf("system prompt lacks the goals: %q", req.Messages[0].Content)
	}
	usage := methods(msgs, "usage")
	if len(usage) != 1 || usage[0].Field("tokensIn") != "11" || usage[0].Field("tokensOut") != "7" || usage[0].Field("weight") != "normal" || usage[0].Field("model") != "autoagents/one-actual" {
		t.Fatalf("usage wrong: %v", usage)
	}
	profile := methods(msgs, "profile.update")
	if len(profile) == 0 || !strings.HasPrefix(profile[0].Field("line"), "Goal: Keep the machine healthy; Hire agents") || !strings.Contains(profile[0].Field("line"), "Now: answering") {
		t.Fatalf("profile wrong: %v", profile)
	}
}

// TestToolLoop checks the tool loop: tools are listed with the scoped token,
// a call runs, its result goes back, and the model answers (PROTOCOL.md T2).
func TestToolLoop(t *testing.T) {
	var step atomic.Int64
	respond := func(gwRequest) reply {
		switch step.Add(1) {
		case 1:
			return reply{Calls: []fakeCall{{ID: "call-1", Name: "add", Args: `{"a":2,"b":3}`}, {ID: "call-2", Name: "fail", Args: `{"a":0,"b":0}`}, {ID: "call-3", Name: "nope", Args: `{`}}}
		default:
			return reply{Text: []string{"done"}}
		}
	}
	r := newRig(t, respond)
	h := conformance.Ready(t, r.subject(), nil)
	_, msgs := h.RunTurn("add please")
	if got := outputText(msgs); got != "done" {
		t.Fatalf("output %q", got)
	}
	reqs := r.gw.requests()
	if len(reqs) != 2 || len(reqs[0].Tools) != 2 || reqs[0].Tools[0].Function.Name != "add" && reqs[0].Tools[1].Function.Name != "add" {
		t.Fatalf("tools not offered: %+v", reqs)
	}
	results := reqs[1].Messages[len(reqs[1].Messages)-3:]
	if results[0].ToolCallID != "call-1" || !strings.Contains(results[0].Content, `"sum":5`) {
		t.Fatalf("add result wrong: %+v", results[0])
	}
	if results[1].ToolCallID != "call-2" || !strings.HasPrefix(results[1].Content, "error: ") || !strings.Contains(results[1].Content, "boom") {
		t.Fatalf("fail result wrong: %+v", results[1])
	}
	if results[2].ToolCallID != "call-3" || !strings.HasPrefix(results[2].Content, "error: ") {
		t.Fatalf("bad call result wrong: %+v", results[2])
	}
	for _, tok := range r.mcp.seenTokens() {
		if tok != mcpToken {
			t.Fatalf("MCP call without the scoped token: %q", tok)
		}
	}
	if got := r.mcp.called(); len(got) != 2 {
		t.Fatalf("tools run: %v", got)
	}
	var seenTool bool
	for _, p := range methods(msgs, "profile.update") {
		seenTool = seenTool || strings.Contains(p.Field("line"), "calling tool add")
	}
	if !seenTool {
		t.Fatalf("profile never named the tool call: %v", methods(msgs, "profile.update"))
	}
}

// TestNoToolsWhenMCPDown carries on without tools and warns in the log.
func TestNoToolsWhenMCPDown(t *testing.T) {
	r := newRig(t, nil)
	sub := r.subject()
	base := sub.Welcome
	sub.Welcome = func(w *conformance.WelcomeSpec) { base(w); w.MCPToken = "wrong-token" }
	h := conformance.Ready(t, sub, nil)
	_, msgs := h.RunTurn("Hello")
	if got := outputText(msgs); got != "reply to: human:alice says: Hello" {
		t.Fatalf("output %q", got)
	}
	logs := methods(msgs, "log")
	if len(logs) == 0 || logs[0].Field("level") != "warn" {
		t.Fatalf("want a warn log, got %v", msgs)
	}
	if len(r.gw.requests()[0].Tools) != 0 {
		t.Fatal("tools offered although the MCP endpoint refused the token")
	}
}

// TestStdioMCPIsRefused: only the streamable-HTTP transport is supported.
func TestStdioMCPIsRefused(t *testing.T) {
	if _, err := openai.DialMCP(context.Background(), sdk.MCP{Transport: "stdio"}, func() sdk.Secret { return "" }, http.DefaultClient); err == nil {
		t.Fatal("stdio transport must be refused")
	}
}

// TestBudgetPauseAndResume checks rule R1 and R2 for the OpenAI runner: a 402
// pauses the turn, the wake after the budget returns resumes it with the same
// history, and no second user message is added.
func TestBudgetPauseAndResume(t *testing.T) {
	r := newRig(t, nil)
	h := conformance.Ready(t, r.subject(), nil)
	turnID, _ := h.RunTurn(promptBudget)
	h.WakeBudgetRestored(turnID)
	msgs := h.Until(conformance.TurnEnded(turnID))
	if got := outputText(msgs); got != "reply to: human:alice says: "+promptBudget {
		t.Fatalf("resumed output %q", got)
	}
	reqs := r.gw.requests()
	if len(reqs) != 2 || len(reqs[1].Messages) != len(reqs[0].Messages) {
		t.Fatalf("the resume must send the same history: %d requests", len(reqs))
	}
}

// TestResumeUnknownTurn answers 1011 when the paused turn is not held (R3).
func TestResumeUnknownTurn(t *testing.T) {
	r := newRig(t, nil)
	h := conformance.Ready(t, r.subject(), nil)
	id := h.WakeBudgetRestored(h.NewTurnID())
	h.ExpectError(&id, sdk.CodeResumeUnavailable, "resume of an unknown turn")
}

// TestModelErrorsEndTheTurn reports 1030 and 1020 as non-fatal errors.
func TestModelErrorsEndTheTurn(t *testing.T) {
	for _, tc := range []struct {
		status int
		code   string
	}{{http.StatusTooManyRequests, "1030"}, {http.StatusInternalServerError, "1020"}} {
		t.Run(tc.code, func(t *testing.T) {
			r := newRig(t, func(gwRequest) reply { return reply{Status: tc.status} })
			h := conformance.Ready(t, r.subject(), nil)
			turnID, msgs := h.RunTurn("Hello")
			errs := methods(msgs, "error")
			if len(errs) != 1 || errs[0].Field("code") != tc.code || errs[0].Field("turnId") != turnID || errs[0].Field("fatal") != "false" {
				t.Fatalf("want one non-fatal %s error, got %v", tc.code, msgs)
			}
		})
	}
}

// TestDeliveriesReachTheModel puts a message delivered into a running turn
// into the next model call (PROTOCOL.md T7).
func TestDeliveriesReachTheModel(t *testing.T) {
	var step atomic.Int64
	release := make(chan struct{})
	respond := func(gwRequest) reply {
		if step.Add(1) == 1 {
			<-release // the first model call waits until the message is delivered
			return reply{Calls: []fakeCall{{ID: "c1", Name: "add", Args: `{"a":1,"b":1}`}}}
		}
		return reply{Text: []string{"ok"}}
	}
	r := newRig(t, respond)
	h := conformance.Ready(t, r.subject(), nil)
	turnID, _ := h.WakeChat("first")
	deliverOnce(t, h, turnID, func() int { return h.Deliver(turnID, "and also this") })
	close(release)
	h.Until(conformance.TurnEnded(turnID))
	reqs := r.gw.requests()
	if len(reqs) != 2 {
		t.Fatalf("want 2 model calls, got %d", len(reqs))
	}
	last := reqs[1].Messages[len(reqs[1].Messages)-1]
	if !strings.Contains(reqs[1].Messages[len(reqs[1].Messages)-3].Content, "and also this") && !strings.Contains(last.Content, "and also this") {
		t.Fatalf("the delivered message never reached the model: %+v", reqs[1].Messages)
	}
}

// TestCancelStopsTheCall cancels a turn whose model call is open (C1).
func TestCancelStopsTheCall(t *testing.T) {
	r := newRig(t, nil)
	h := conformance.Ready(t, r.subject(), nil)
	turnID, _ := h.WakeChat(promptHold)
	h.Until(func(m conformance.Msg) bool { return m.Method == "output" })
	h.Cancel(turnID)
	msgs := h.Until(conformance.TurnEnded(turnID))
	if len(methods(msgs, "error")) != 0 {
		t.Fatalf("a cancelled turn must not report an error: %v", msgs)
	}
}

// TestTaskReportsDone ends a task turn with task.done.
func TestTaskReportsDone(t *testing.T) {
	r := newRig(t, nil)
	h := conformance.Ready(t, r.subject(), nil)
	turnID, _ := h.WakeTask("Write notes", "Please write notes.")
	msgs := h.Until(conformance.TurnEnded(turnID))
	done := methods(msgs, "task.done")
	if len(done) != 1 || done[0].Field("outcome") != "done" || !strings.Contains(done[0].Field("summary"), "Write notes") && !strings.Contains(done[0].Field("summary"), "reply") {
		t.Fatalf("task.done wrong: %v", msgs)
	}
}

// TestStepLimit stops a model that never stops calling tools.
func TestStepLimit(t *testing.T) {
	r := newRig(t, func(gwRequest) reply {
		return reply{Calls: []fakeCall{{ID: "c", Name: "add", Args: `{"a":1,"b":1}`}}}
	})
	sub := r.subject()
	sub.Start = conformance.InProcess(func(ctx context.Context, socket string) error {
		opts := openai.Options()
		opts.Socket = socket
		runner := openai.New(openai.Config{MaxSteps: 3})
		defer func() { _ = runner.Close() }()
		return sdk.Run(ctx, opts, runner)
	})
	h := conformance.Ready(t, sub, nil)
	_, msgs := h.RunTurn("loop")
	if got := outputText(msgs); !strings.Contains(got, "Stopped after 3 model calls") || len(r.gw.requests()) != 3 {
		t.Fatalf("output %q after %d calls", got, len(r.gw.requests()))
	}
}

// deliverOnce waits until the turn runs (status working), sends one deliver
// and requires that the runner accepted it.
func deliverOnce(t *testing.T, h *conformance.H, turnID string, send func() int) {
	t.Helper()
	h.Until(func(m conformance.Msg) bool {
		return m.Method == "status" && m.Field("state") == "working" && m.Field("turnId") == turnID
	})
	if m := h.AwaitResponse(send()); m.Error != nil {
		t.Fatalf("deliver was refused: %s", m)
	}
}

func busMessage(subject, body string) map[string]any {
	return map[string]any{"id": "msg_01JD8X3Q5Y6Z7A8B9C0D1E2FAB", "from": "agent:hugo", "to": "agent:founder", "subject": subject, "body": body,
		"sentAt": "2026-11-19T19:00:02Z", "replyTo": nil, "correlationId": nil}
}

// TestWakeKinds checks the text the model reads and the profile line for each
// wake reason (PROTOCOL.md T1, T3).
func TestWakeKinds(t *testing.T) {
	note := "check the disk"
	cases := []struct {
		name    string
		payload map[string]any
		reason  string
		prompt  string
		now     string
	}{
		{"message", busMessage("Disk", "is full"), "message", `Message from agent:hugo, subject "Disk": is full`, "reading a message from agent:hugo"},
		{"event", map[string]any{"specversion": "1.0", "id": "evt_01JD8X3Q5Y6Z7A8B9C0D1E2FAB", "source": "/emitters/disk", "type": "disk.full",
			"subject": "founder", "time": "2026-11-19T19:05:00Z", "datacontenttype": "application/json", "data": map[string]any{"pct": 99},
			"sequence": "00000000000000000043", "actor": "host:agentsone", "chartscope": "agent:founder"}, "event", `Event disk.full: {"pct":99}`, "handling event disk.full"},
		{"timer", map[string]any{"scheduleId": "sch_01JD8X3Q5Y6Z7A8B9C0D1E2FAB", "name": "daily", "due": "2026-11-19T19:00:00Z", "firedAt": "2026-11-19T19:00:01Z", "note": note},
			"timer", "Timer daily fired. " + note, "handling timer daily"},
		{"task", map[string]any{"id": "tsk_01JD8X3Q5Y6Z7A8B9C0D1E2FAB", "title": "Clean up", "body": "Remove logs.", "from": "human:alice", "createdAt": "2026-11-19T19:01:00Z"},
			"task", "Task from human:alice: Clean up\nRemove logs.", "working on task Clean up"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t, nil)
			h := conformance.Ready(t, r.subject(), nil)
			turnID := h.NewTurnID()
			h.Request("wake", map[string]any{"reason": tc.reason, "turnId": turnID, "payload": tc.payload})
			msgs := h.Until(conformance.TurnEnded(turnID))
			if got := r.gw.requests()[0].last().Content; got != tc.prompt {
				t.Fatalf("model read %q, want %q", got, tc.prompt)
			}
			if p := methods(msgs, "profile.update"); len(p) == 0 || !strings.Contains(p[0].Field("line"), "Now: "+tc.now) {
				t.Fatalf("profile wrong: %v", p)
			}
		})
	}
}

// TestBusDeliveryReachesTheModel names the sender of a delivered bus message
// and keeps the message in the history for the compaction call.
func TestBusDeliveryReachesTheModel(t *testing.T) {
	var step atomic.Int64
	release := make(chan struct{})
	respond := func(req gwRequest) reply {
		if strings.HasPrefix(req.last().Content, "Summarise this session") {
			return reply{Text: []string{"Summary: we talked."}}
		}
		if step.Add(1) == 1 {
			<-release
			return reply{Calls: []fakeCall{{ID: "c1", Name: "add", Args: `{"a":1,"b":1}`}}}
		}
		return reply{Text: []string{"ok"}}
	}
	r := newRig(t, respond)
	h := conformance.Ready(t, r.subject(), nil)
	turnID, _ := h.WakeChat("first")
	deliverOnce(t, h, turnID, func() int {
		return h.Request("deliver", map[string]any{"kind": "bus", "turnId": turnID, "message": busMessage("Hi", "psst")})
	})
	close(release)
	h.Until(conformance.TurnEnded(turnID))
	reqs := r.gw.requests()
	var seen bool
	for _, m := range reqs[1].Messages {
		seen = seen || m.Content == "New message from agent:hugo while you work: psst"
	}
	if !seen {
		t.Fatalf("bus message not in the second call: %+v", reqs[1].Messages)
	}
	if got := sleepSummary(t, h, 256); got != "Summary: we talked." {
		t.Fatalf("summary %q", got)
	}
	last := r.gw.requests()
	if c := last[len(last)-1].last().Content; !strings.Contains(c, "assistant called add({\"a\":1,\"b\":1})") || !strings.Contains(c, "tool: {\"sum\":2}") {
		t.Fatalf("compaction transcript lacks the tool call: %q", c)
	}
}

func sleepSummary(t *testing.T, h *conformance.H, limit int) string {
	t.Helper()
	m := h.AwaitResponse(h.Request("sleep", map[string]any{"reason": "idle", "maxSummaryChars": limit}))
	var res struct {
		Summary string `json:"summary"`
	}
	if err := json.Unmarshal(m.Result, &res); err != nil {
		t.Fatalf("sleep result %s: %v", m, err)
	}
	return res.Summary
}

// TestSleepCompactsWithAQuickCall checks rule S1: one quick model call makes
// the summary, which is cut to the limit (PROTOCOL.md S1).
func TestSleepCompactsWithAQuickCall(t *testing.T) {
	r := newRig(t, nil)
	h := conformance.Ready(t, r.subject(), nil)
	h.RunTurn("Hello")
	if got := sleepSummary(t, h, 256); got != "Summary: we talked." {
		t.Fatalf("summary %q", got)
	}
	reqs := r.gw.requests()
	last := reqs[len(reqs)-1]
	if last.Weight != "quick" || len(last.Tools) != 0 || !strings.Contains(last.last().Content, "human:alice says: Hello") {
		t.Fatalf("compaction call wrong: %+v", last)
	}
}

// TestSleepFallsBackToTruncation cuts the transcript when the model call fails,
// and an empty session sleeps without a call.
func TestSleepFallsBackToTruncation(t *testing.T) {
	var broke atomic.Bool
	r := newRig(t, nil)
	r.gw = newGateway(t, func(gwRequest) reply {
		if broke.Load() {
			return reply{Status: http.StatusPaymentRequired}
		}
		return reply{Text: []string{"fine"}}
	})
	h := conformance.Ready(t, r.subject(), nil)
	if got := sleepSummary(t, h, 256); got != "No activity yet." {
		t.Fatalf("empty session summary %q", got)
	}
	h.Next() // status sleeping
	h.RunTurn("remember the blue folder " + strings.Repeat("and more ", 60))
	broke.Store(true)
	got := sleepSummary(t, h, 256)
	if len([]rune(got)) > 256 || !strings.HasPrefix(got, "Transcript cut to fit.") || !strings.HasSuffix(strings.TrimSpace(got), "assistant: fine") {
		t.Fatalf("fallback summary %q", got)
	}
}

// TestSessionSummarySeedsTheModel puts the welcome's summary into the system
// prompt (S2) and keeps a summary across a second sleep.
func TestSessionSummarySeedsTheModel(t *testing.T) {
	r := newRig(t, nil)
	h := conformance.Ready(t, r.subject(), func(w *conformance.WelcomeSpec) { s := "We chose the blue folder."; w.Summary = &s })
	h.RunTurn("Hello")
	if sys := r.gw.requests()[0].Messages[0].Content; !strings.Contains(sys, "We chose the blue folder.") {
		t.Fatalf("summary missing from the system prompt: %q", sys)
	}
	if got := sleepSummary(t, h, 256); got != "Summary: we talked." {
		t.Fatalf("summary %q", got)
	}
}
