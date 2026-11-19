// SPDX-License-Identifier: MIT

package claude

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/runners/conformance"
	"github.com/SDK-E/agentsone/runners/sdk"
)

func TestMain(m *testing.M) {
	switch {
	case isFakeChild():
		<-time.After(time.Hour)
		os.Exit(0)
	case isFakeEngine():
		os.Exit(runFakeEngine())
	}
	os.Exit(m.Run())
}

const (
	promptHold   = "HOLD"
	promptBudget = "BUDGET"
	promptFreeze = "FREEZE"
	promptWait   = "WAIT"
	welcomeMCP   = "test-mcp-token-founder-0001" // the made-up token of the PROTOCOL.md transcript
)

var longRe = regexp.MustCompile(`LONG (\d+)`)

// rig is the fakes and the subject that points the runner at them.
type rig struct {
	gw      *fakeGateway
	mcp     *fakeMCP
	tokens  atomic.Int64
	mode    string
	engine  string
	states  string
	release chan struct{}

	mu   sync.Mutex
	logs []string
	dirs []string
}

func newRig(t *testing.T, respond func(gwRequest) gwReply) *rig {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	r := &rig{mcp: newMCP(t, welcomeMCP), engine: self, states: t.TempDir(), release: make(chan struct{})}
	if respond == nil {
		respond = r.scripted
	}
	r.gw = newGateway(t, respond)
	return r
}

// scripted is the default fake model.
func (r *rig) scripted(req gwRequest) gwReply {
	last := req.Last
	switch {
	case strings.HasPrefix(last, askSummary):
		return gwReply{Text: []string{"Summary: we talked."}}
	case strings.Contains(last, promptHold):
		return gwReply{Hold: true}
	case strings.Contains(last, promptWait):
		return gwReply{Wait: r.release, Text: []string{"first answer"}}
	case strings.Contains(last, promptBudget) && r.gw.firstTime(req.Auth):
		return gwReply{Status: http.StatusPaymentRequired}
	case strings.Contains(last, promptFreeze):
		return gwReply{Status: http.StatusLocked}
	case strings.Contains(last, "RATE"):
		return gwReply{Status: http.StatusTooManyRequests}
	case strings.Contains(last, "BROKEN"):
		return gwReply{Status: http.StatusBadGateway}
	case longRe.MatchString(last):
		n, _ := strconv.Atoi(longRe.FindStringSubmatch(last)[1])
		var parts []string
		for ; n > 0; n -= 4096 {
			parts = append(parts, strings.Repeat("x", min(n, 4096)))
		}
		return gwReply{Text: parts}
	}
	return gwReply{Text: []string{"reply to: ", last}}
}

func (r *rig) log(line string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.logs = append(r.logs, line)
}

func (r *rig) logLines() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.logs)
}

// stateDir makes the state folder of one runner start, with the fake mode.
func (r *rig) stateDir() (string, error) {
	dir, err := os.MkdirTemp(r.states, "s")
	if err != nil {
		return "", err
	}
	if r.mode != "" {
		if err := os.WriteFile(filepath.Join(dir, fakeModeFile), []byte(r.mode), 0o600); err != nil {
			return "", err
		}
	}
	r.mu.Lock()
	r.dirs = append(r.dirs, dir)
	r.mu.Unlock()
	return dir, nil
}

func (r *rig) subject() conformance.Subject {
	return conformance.Subject{
		Name: "claude",
		Start: conformance.InProcess(func(ctx context.Context, socket string) error {
			dir, err := r.stateDir()
			if err != nil {
				return err
			}
			opts := Options()
			opts.Socket = socket
			runner := New(Config{EnginePath: r.engine, StateDir: dir, GraceMs: 1000, Log: r.log})
			defer func() { _ = runner.Close() }()
			return sdk.Run(ctx, opts, runner)
		}),
		Prompts: conformance.Prompts{
			Plain:  "Hello",
			Hold:   promptHold,
			Budget: promptBudget,
			Long:   func(n int) string { return fmt.Sprintf("LONG %d", n) },
		},
		// The runner puts the summary in the engine's system prompt;
		// TestWelcomeSummaryStartsTheSession proves rule S2.
		SummaryInOutput: false,
		Welcome: func(w *conformance.WelcomeSpec) {
			w.ModelBaseURL = r.gw.URL()
			w.ModelToken = fmt.Sprintf("gw-token-%d", r.tokens.Add(1))
			w.MCPURL = r.mcp.srv.URL
		},
	}
}

// records are the starts of the fake engine, oldest first, of the only
// runner the rig started.
func (r *rig) records(t *testing.T) []engineRecord {
	t.Helper()
	r.mu.Lock()
	dirs := slices.Clone(r.dirs)
	r.mu.Unlock()
	var out []engineRecord
	for _, dir := range dirs {
		files, _ := filepath.Glob(filepath.Join(dir, "record-*.json"))
		slices.Sort(files)
		for _, f := range files {
			b, err := os.ReadFile(f) //nolint:gosec // G304: a record in the test's own folder
			if err != nil {
				t.Fatal(err)
			}
			var rec engineRecord
			if err := json.Unmarshal(b, &rec); err != nil {
				t.Fatal(err)
			}
			out = append(out, rec)
		}
	}
	return out
}

func (rec engineRecord) env(key string) string {
	for _, kv := range rec.Env {
		if k, v, _ := strings.Cut(kv, "="); k == key {
			return v
		}
	}
	return ""
}

func (rec engineRecord) flag(name string) string {
	i := slices.Index(rec.Args, name)
	if i < 0 || i+1 >= len(rec.Args) {
		return ""
	}
	return rec.Args[i+1]
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

// waitGone waits until pid is no longer a process.
func waitGone(t *testing.T, what string, pid int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for syscall.Kill(pid, 0) == nil {
		if time.Now().After(deadline) {
			t.Fatalf("%s (pid %d) is still running", what, pid)
		}
		<-time.After(20 * time.Millisecond)
	}
}

// TestConformance runs every rule of PROTOCOL.md against the runner with
// the fake engine, a fake gateway and a fake MCP server (TESTING.md B4c).
func TestConformance(t *testing.T) {
	r := newRig(t, nil)
	conformance.Run(t, r.subject())
}

// TestPlainTurn follows one turn from the wake to the gateway and back: the
// engine calls the proxy with the local secret, and the gateway gets the
// welcome token, the model and the weight (T3, T5, T6).
func TestPlainTurn(t *testing.T) {
	r := newRig(t, nil)
	h := conformance.Ready(t, r.subject(), nil)
	_, msgs := h.RunTurn("Hello")
	if got := outputText(msgs); got != "reply to: human:alice says: Hello" {
		t.Fatalf("output %q", got)
	}
	reqs := r.gw.requests()
	if len(reqs) != 1 {
		t.Fatalf("gateway calls: %+v", reqs)
	}
	req := reqs[0]
	if req.Auth != "Bearer gw-token-1" || req.Query != "beta=true" || req.APIKey != "" || req.Model != "autoagents/one:normal" {
		t.Fatalf("gateway request wrong: %+v", req)
	}
	if !strings.Contains(req.System, "Hire agents") || !strings.Contains(req.System, "You are founder") {
		t.Fatalf("system prompt lacks the agent: %q", req.System)
	}
	usage := methods(msgs, "usage")
	if len(usage) != 1 || usage[0].Field("tokensIn") != "11" || usage[0].Field("tokensOut") != "7" ||
		usage[0].Field("cacheReadTokens") != "3" || usage[0].Field("weight") != "normal" || usage[0].Field("model") != "autoagents/one-actual" {
		t.Fatalf("usage wrong: %v", usage)
	}
	profile := methods(msgs, "profile.update")
	if len(profile) == 0 || !strings.HasPrefix(profile[0].Field("line"), "Goal: Keep the machine healthy; Hire agents") ||
		!strings.Contains(profile[0].Field("line"), "Now: answering human:alice") {
		t.Fatalf("profile wrong: %v", profile)
	}
	recs := r.records(t)
	if len(recs) != 1 {
		t.Fatalf("engine starts: %d", len(recs))
	}
	secret := recs[0].env("ANTHROPIC_AUTH_TOKEN")
	if len(secret) != 64 {
		t.Fatalf("the engine's bearer is not the local secret: %q", secret)
	}
	assertNoSecrets(t, r, h, recs, secret)
	// The engine's stderr reaches the log, redacted.
	deadline := time.Now().Add(5 * time.Second)
	for !slices.ContainsFunc(r.logLines(), func(l string) bool { return strings.Contains(l, "fake engine up with auth [redacted]") }) {
		if time.Now().After(deadline) {
			t.Fatalf("the engine's stderr never reached the log: %q", r.logLines())
		}
		<-time.After(10 * time.Millisecond)
	}
	assertNoSecrets(t, r, h, recs, secret)
}

// assertNoSecrets checks that neither real token reached the engine and that
// no token or local secret is in any runner line or log line.
func assertNoSecrets(t *testing.T, r *rig, h *conformance.H, recs []engineRecord, secret string) {
	t.Helper()
	tokens := []string{"gw-token-1", welcomeMCP}
	for _, rec := range recs {
		all := strings.Join(rec.Args, " ") + " " + strings.Join(rec.Env, " ")
		for _, tok := range tokens {
			if strings.Contains(all, tok) {
				t.Fatalf("the engine saw the real token %q", tok)
			}
		}
		if strings.Contains(strings.Join(rec.Args, " "), secret) {
			t.Fatal("the local secret is in the engine's arguments")
		}
	}
	lines := r.logLines()
	for _, m := range h.Log {
		lines = append(lines, m.String())
	}
	for _, l := range lines {
		for _, s := range append(tokens, secret) {
			if strings.Contains(l, s) {
				t.Fatalf("a secret is in a runner line: %q", l)
			}
		}
	}
}

// TestToolCallThroughProxy has the engine call a machine tool: the call
// goes through the proxy with the MCP token, and the profile names it.
func TestToolCallThroughProxy(t *testing.T) {
	r := newRig(t, nil)
	h := conformance.Ready(t, r.subject(), nil)
	_, msgs := h.RunTurn("TOOL please")
	tokens, calls := r.mcp.seen()
	if len(calls) != 1 || len(tokens) == 0 {
		t.Fatalf("tool calls %v, tokens %d", calls, len(tokens))
	}
	for _, tok := range tokens {
		if tok != welcomeMCP {
			t.Fatalf("MCP call without the scoped token: %q", tok)
		}
	}
	if reqs := r.gw.requests(); len(reqs) != 1 || !strings.Contains(reqs[0].Last, `{"sum":5}`) {
		t.Fatalf("the tool result did not reach the model: %+v", reqs)
	}
	if !slices.ContainsFunc(methods(msgs, "profile.update"), func(m conformance.Msg) bool {
		return strings.Contains(m.Field("line"), "calling tool mcp__machine__add")
	}) {
		t.Fatalf("profile never named the tool: %v", methods(msgs, "profile.update"))
	}
}

// TestNoNetworkButGateway points every proxy variable and the Anthropic
// variables of the runner at a trap. The engine and the runner reach only
// the gateway and the MCP endpoint, through the token proxy. It uses
// t.Setenv, so it does not run in parallel.
func TestNoNetworkButGateway(t *testing.T) {
	tr := newTrap(t)
	for _, k := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "ANTHROPIC_BASE_URL"} {
		t.Setenv(k, tr.srv.URL)
	}
	t.Setenv("ANTHROPIC_API_KEY", "trap-api-key")
	t.Setenv("CLAUDE_CODE_USE_BEDROCK", "1")
	r := newRig(t, nil)
	h := conformance.Ready(t, r.subject(), nil)
	_, msgs := h.RunTurn("TOOL please")
	if got := outputText(msgs); !strings.Contains(got, "reply to: ") {
		t.Fatalf("output %q", got)
	}
	if n := tr.hits.Load(); n != 0 {
		t.Fatalf("the trap got %d requests", n)
	}
	if _, calls := r.mcp.seen(); len(r.gw.requests()) != 1 || len(calls) != 1 {
		t.Fatalf("gateway calls %d, tool calls %v", len(r.gw.requests()), calls)
	}
	rec := r.records(t)[0]
	for _, k := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "ANTHROPIC_API_KEY", "CLAUDE_CODE_USE_BEDROCK"} {
		if v := rec.env(k); v != "" {
			t.Fatalf("the engine inherited %s=%s", k, v)
		}
	}
}

// errorMsg is the first error notification of msgs.
func errorMsg(t *testing.T, msgs []conformance.Msg) conformance.Msg {
	t.Helper()
	errs := methods(msgs, "error")
	if len(errs) != 1 {
		t.Fatalf("want one error, got %v", msgs)
	}
	return errs[0]
}

// TestEngineMissing fails each turn with the plain licence message while
// the engine is not installed; the runner keeps serving.
func TestEngineMissing(t *testing.T) {
	r := newRig(t, nil)
	r.engine = filepath.Join(t.TempDir(), "claude")
	h := conformance.Ready(t, r.subject(), nil)
	for range 2 {
		_, msgs := h.RunTurn("Hello")
		e := errorMsg(t, msgs)
		if e.Field("code") != strconv.Itoa(sdk.CodeModelUnavailable) || e.Field("fatal") != "false" ||
			e.Field("message") != "The Claude engine is not installed yet. A human must accept its licence on the setup page; the download starts then." {
			t.Fatalf("error wrong: %s", e)
		}
	}
	m := h.AwaitResponse(h.Request("sleep", map[string]any{"reason": "idle", "maxSummaryChars": 256}))
	if m.Error != nil {
		t.Fatalf("sleep without an engine: %s", m)
	}
}

// TestRootRefusal turns the engine's refusal to run as root into a plain
// error, and the stderr line is logged without the secret.
func TestRootRefusal(t *testing.T) {
	r := newRig(t, nil)
	r.mode = "root"
	h := conformance.Ready(t, r.subject(), nil)
	_, msgs := h.RunTurn("Hello")
	e := errorMsg(t, msgs)
	if !strings.Contains(e.Field("message"), "refused to run as root") {
		t.Fatalf("error wrong: %s", e)
	}
	recs := r.records(t)
	assertNoSecrets(t, r, h, recs, recs[0].env("ANTHROPIC_AUTH_TOKEN"))
}

// TestFreeze maps the gateway's 423 to a plain error that does not pause
// the turn.
func TestFreeze(t *testing.T) {
	r := newRig(t, nil)
	h := conformance.Ready(t, r.subject(), nil)
	_, msgs := h.RunTurn(promptFreeze)
	e := errorMsg(t, msgs)
	if e.Field("code") != "1020" || e.Field("fatal") != "false" || e.Field("message") != "A human froze model calls" {
		t.Fatalf("error wrong: %s", e)
	}
	if last := msgs[len(msgs)-1]; last.Field("pausedTurnId") != "" {
		t.Fatalf("a freeze must not pause the turn: %s", last)
	}
}

// TestCancelKillsDeafEngine cancels a turn whose engine ignores the
// interrupt: the engine is killed, the turn is idle within 5 s, and the next
// wake resumes the same session.
func TestCancelKillsDeafEngine(t *testing.T) {
	r := newRig(t, nil)
	r.mode = "ignore-interrupt"
	h := conformance.Ready(t, r.subject(), nil)
	turn, _ := h.WakeChat(promptHold)
	h.Until(func(m conformance.Msg) bool { return m.Method == "output" })
	start := time.Now()
	h.AwaitResponse(h.Cancel(turn))
	h.Until(conformance.TurnEnded(turn))
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("the cancelled turn took %s to end", d)
	}
	first := r.records(t)[0]
	waitGone(t, "the engine", first.Pid)
	_, msgs := h.RunTurn("Hello")
	if got := outputText(msgs); !strings.Contains(got, "reply to: ") {
		t.Fatalf("output after the restart %q", got)
	}
	recs := r.records(t)
	if len(recs) != 2 || recs[1].flag("--resume") == "" || recs[1].flag("--resume") != first.flag("--session-id") {
		t.Fatalf("the restart must resume session %q: %v", first.flag("--session-id"), recs[len(recs)-1].Args)
	}
}

// TestCancelInterruptsEngine keeps the engine when it answers the
// interrupt.
func TestCancelInterruptsEngine(t *testing.T) {
	r := newRig(t, nil)
	h := conformance.Ready(t, r.subject(), nil)
	turn, _ := h.WakeChat(promptHold)
	h.Until(func(m conformance.Msg) bool { return m.Method == "output" })
	h.AwaitResponse(h.Cancel(turn))
	h.Until(conformance.TurnEnded(turn))
	h.RunTurn("Hello")
	if recs := r.records(t); len(recs) != 1 {
		t.Fatalf("an answered interrupt must keep the engine, got %d starts", len(recs))
	}
}

func sleep(t *testing.T, h *conformance.H, maxChars int) string {
	t.Helper()
	m := h.AwaitResponse(h.Request("sleep", map[string]any{"reason": "idle", "maxSummaryChars": maxChars}))
	var res struct {
		Summary string `json:"summary"`
	}
	if m.Error != nil || json.Unmarshal(m.Result, &res) != nil {
		t.Fatalf("sleep: %s", m)
	}
	return res.Summary
}

// TestSleepCutsSummary cuts the engine's summary to maxSummaryChars, ends
// the session, and starts the next one with the summary in the system
// prompt.
func TestSleepCutsSummary(t *testing.T) {
	r := newRig(t, nil)
	base := r.scripted
	r.gw.respond = func(req gwRequest) gwReply {
		if strings.HasPrefix(req.Last, askSummary) {
			return gwReply{Text: []string{strings.Repeat("s", 500)}}
		}
		return base(req)
	}
	h := conformance.Ready(t, r.subject(), nil)
	h.RunTurn("Hello")
	summary := sleep(t, h, 256)
	if summary != strings.Repeat("s", 256) {
		t.Fatalf("summary %q", summary)
	}
	h.RunTurn("Hello again")
	recs := r.records(t)
	if len(recs) != 2 || recs[1].flag("--session-id") == "" || recs[1].flag("--session-id") == recs[0].flag("--session-id") {
		t.Fatalf("after sleep the engine must start a new session: %v", recs)
	}
	reqs := r.gw.requests()
	if !strings.Contains(reqs[len(reqs)-1].System, summary) {
		t.Fatalf("the new session lacks the summary: %q", reqs[len(reqs)-1].System)
	}
}

// TestSleepFallsBackToTranscript uses the cut transcript when the summary
// call is refused.
func TestSleepFallsBackToTranscript(t *testing.T) {
	r := newRig(t, nil)
	base := r.scripted
	r.gw.respond = func(req gwRequest) gwReply {
		if strings.HasPrefix(req.Last, askSummary) {
			return gwReply{Status: http.StatusPaymentRequired}
		}
		return base(req)
	}
	h := conformance.Ready(t, r.subject(), nil)
	h.RunTurn("LONG 600")
	summary := sleep(t, h, 256)
	if len([]rune(summary)) != 256 || !strings.HasPrefix(summary, "Transcript cut to fit.\n") || !strings.HasSuffix(summary, "xxxxx") {
		t.Fatalf("summary %q", summary)
	}
}

// TestWelcomeSummaryStartsTheSession proves rule S2: the welcome's summary
// is in the engine's system prompt.
func TestWelcomeSummaryStartsTheSession(t *testing.T) {
	r := newRig(t, nil)
	const summary = "We talked about the weekly status note."
	h := conformance.Ready(t, r.subject(), func(w *conformance.WelcomeSpec) { v := summary; w.Summary = &v })
	h.RunTurn("Hello")
	if reqs := r.gw.requests(); !strings.Contains(reqs[0].System, summary) {
		t.Fatalf("system prompt lacks the summary: %q", reqs[0].System)
	}
}

// TestDeliveryAfterFirstResult sends a message delivered during a turn to
// the engine after its first result, in the same turn.
func TestDeliveryAfterFirstResult(t *testing.T) {
	r := newRig(t, nil)
	h := conformance.Ready(t, r.subject(), nil)
	turn, _ := h.WakeChat(promptWait)
	h.Until(func(m conformance.Msg) bool { return m.Method == "status" && m.Field("state") == "working" })
	h.AwaitResponse(h.Deliver(turn, "Are you there?"))
	close(r.release)
	msgs := h.Until(conformance.TurnEnded(turn))
	want := "first answerreply to: New message from human:alice while you work: Are you there?"
	if got := outputText(msgs); got != want {
		t.Fatalf("output %q, want %q", got, want)
	}
	if n := len(methods(msgs, "usage")); n != 2 {
		t.Fatalf("usage reports: %d", n)
	}
}

// TestTaskDone reports a task done with the engine's final text.
func TestTaskDone(t *testing.T) {
	r := newRig(t, nil)
	h := conformance.Ready(t, r.subject(), nil)
	turn, _ := h.WakeTask("Write the note", "Please write it.")
	msgs := h.Until(conformance.TurnEnded(turn))
	done := methods(msgs, "task.done")
	if len(done) != 1 || done[0].Field("outcome") != "done" || !strings.Contains(done[0].Field("summary"), "Write the note") {
		t.Fatalf("task.done wrong: %v", done)
	}
}

// TestNoEngineLeftBehind stops the runner with an engine that ignores the
// closed stdin and has a child: neither process outlives the runner.
func TestNoEngineLeftBehind(t *testing.T) {
	for _, tc := range []struct {
		name     string
		stop     func(h *conformance.H)
		wantZero bool
	}{
		{"shutdown", func(h *conformance.H) {
			h.AwaitResponse(h.Request("shutdown", map[string]any{"reason": "stop", "graceMs": 5000}))
			h.ReadUntilEOF()
		}, true},
		{"socket closed", func(h *conformance.H) { h.CloseSocket() }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t, nil)
			r.mode = "linger child"
			h := conformance.Ready(t, r.subject(), nil)
			h.RunTurn("Hello")
			rec := r.records(t)[0]
			b, err := os.ReadFile(filepath.Join(filepath.Dir(rec.flag("--mcp-config")), fakeChildFile))
			if err != nil {
				t.Fatal(err)
			}
			child, _ := strconv.Atoi(string(b))
			tc.stop(h)
			if code := h.WaitExit(5 * time.Second); (code == 0) != tc.wantZero {
				t.Fatalf("exit status %d", code)
			}
			waitGone(t, "the engine", rec.Pid)
			waitGone(t, "the engine's child", child)
		})
	}
}

func welcome(mut func(*sdk.Welcome)) sdk.Welcome {
	w := sdk.Welcome{
		Agent:       sdk.Agent{Name: "ada"},
		MCP:         sdk.MCP{Transport: "streamable-http", URL: "http://127.0.0.1:7700/mcp"},
		Model:       sdk.Model{BaseURL: "http://edge:4000/v1", DefaultWeight: "normal"},
		Credentials: &sdk.Credentials{},
	}
	mut(&w)
	return w
}

// TestStartRefusals fails Start with a plain error for what the runner
// cannot serve safely.
func TestStartRefusals(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		mut        func(*sdk.Welcome)
	}{
		{"stdio MCP", "stdio", func(w *sdk.Welcome) { w.MCP = sdk.MCP{Transport: "stdio", Command: "/usr/bin/agentctl"} }},
		{"unknown config key", "unknown key", func(w *sdk.Welcome) { w.Config.Runner = map[string]any{"allowTools": []any{"Read"}} }},
		{"bad base URL", "model base URL", func(w *sdk.Welcome) { w.Model.BaseURL = "ftp://edge" }},
		{"bad MCP URL", "MCP URL", func(w *sdk.Welcome) { w.MCP.URL = "edge" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := New(Config{StateDir: t.TempDir()})
			err := r.Start(context.Background(), welcome(tc.mut))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Start error %v, want %q", err, tc.want)
			}
			if err := r.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// TestDefaults fills the engine path, the state folder and the grace.
func TestDefaults(t *testing.T) {
	t.Setenv("HOME", "/home/ada")
	r := New(Config{})
	if r.cfg.StateDir != "/home/ada/.local/state/agentsone/claude" || r.cfg.GraceMs != DefaultGraceMs || r.cfg.Log == nil {
		t.Fatalf("defaults %+v", r.cfg)
	}
	if !strings.HasPrefix(r.cfg.EnginePath, "/var/lib/agentsone-engines/") {
		t.Fatalf("engine path %q", r.cfg.EnginePath)
	}
	if !errors.Is(r.Close(), nil) {
		t.Fatal("close of a runner that never started")
	}
}
