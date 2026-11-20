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
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/SDK-E/agentsone/internal/enginefetch/domain"
	"github.com/SDK-E/agentsone/runners/sdk"
)

// Version is the runner version sent in hello. It renews its tokens on
// protocol 1.1 without a restart: the proxy reads them per request.
const Version = "1.1.0"

// DefaultGraceMs is how long the engine gets to answer an interrupt, and to
// exit after its stdin closes, before its process group is killed.
const DefaultGraceMs = 2000

const (
	weightNormal = "normal"
	// LicenceMessage is the turn error while the engine is not installed.
	licenceMessage = "The Claude engine is not installed yet. A human must accept its licence on the setup page; the download starts then."
	rootMessage    = "The Claude engine refused to run as root outside a recognised sandbox."
	frozenMessage  = "A human froze model calls"
	budgetBack     = "Budget is back. Continue the work you were doing."
	askSummary     = "Summarise this session"
	cutNote        = "Transcript cut to fit.\n"
	// rootRefusal is what Claude Code prints when it will not bypass
	// permissions as root.
	rootRefusal = "cannot be used with root/sudo privileges"
	// maxTranscript bounds the lines kept for the fallback summary.
	maxTranscript = 200
)

// Options returns the SDK options of the runner.
func Options() sdk.Options {
	return sdk.Options{
		Runner:       sdk.Info{Name: "claude", Version: Version},
		Capabilities: sdk.Capabilities{Streaming: true, Cancel: true, Compaction: true, Resume: true},
	}
}

// Config holds what tests and the main package may change.
type Config struct {
	// EnginePath is the Claude Code binary. Default: the pinned release in
	// the machine's engines folder.
	EnginePath string
	// StateDir is the engine's CLAUDE_CONFIG_DIR and the folder of its
	// 0600 launch files. Default: $HOME/.local/state/agentsone/claude.
	StateDir string
	// GraceMs is the engine's time to answer an interrupt or to exit.
	// Default: DefaultGraceMs.
	GraceMs int
	// Log gets the runner's own lines: redacted engine stderr and proxy
	// failures. Default: the runner's stderr.
	Log func(line string)
}

// Runner is the handler of the Claude runner: it drives one long-lived
// Claude Code engine through the token proxy.
type Runner struct {
	cfg Config

	// exchange runs one engine exchange at a time.
	exchange sync.Mutex

	mu          sync.Mutex
	welcome     sdk.Welcome
	creds       *sdk.Credentials
	rules       permissionRules
	proxy       *proxy
	redact      func(string) string
	weight      string
	agentPrompt string
	summary     string
	sessionID   string
	resumable   bool // the session was started once: restart it with --resume
	eng         *engine
	paused      map[string]*sdk.Task
	transcript  []string
	closed      bool
}

var (
	_ sdk.Handler = (*Runner)(nil)
	_ sdk.Resumer = (*Runner)(nil)
)

// New returns a runner. The zero Config is fine.
func New(cfg Config) *Runner {
	if cfg.EnginePath == "" {
		if pin, err := domain.Pinned(); err == nil {
			cfg.EnginePath = domain.BinaryPath(domain.DefaultRoot, pin)
		}
	}
	if cfg.StateDir == "" {
		if home, err := os.UserHomeDir(); err == nil {
			cfg.StateDir = filepath.Join(home, ".local", "state", "agentsone", "claude")
		}
	}
	if cfg.GraceMs <= 0 {
		cfg.GraceMs = DefaultGraceMs
	}
	if cfg.Log == nil {
		cfg.Log = func(line string) { fmt.Fprintln(os.Stderr, line) }
	}
	return &Runner{cfg: cfg, paused: map[string]*sdk.Task{}, redact: func(s string) string { return s }}
}

func (r *Runner) grace() time.Duration { return time.Duration(r.cfg.GraceMs) * time.Millisecond }

// Start checks the welcome and starts the token proxy. The engine starts on
// the first wake, so a missing engine never fails Start.
func (r *Runner) Start(_ context.Context, w sdk.Welcome) error {
	if w.MCP.Transport == "stdio" {
		// A stdio MCP server would need the real token in the engine's
		// environment, which the token wall forbids.
		return errors.New("the Claude runner cannot use a stdio MCP server; it needs the machine MCP endpoint over HTTP")
	}
	rules, err := parseRunnerConfig(w.Config.Runner)
	if err != nil {
		return err
	}
	creds := w.Credentials
	if creds == nil {
		creds = &sdk.Credentials{}
	}
	p, err := startProxy(proxyConfig{ModelBaseURL: w.Model.BaseURL, MCPURL: w.MCP.URL, Tokens: creds, Log: r.cfg.Log})
	if err != nil {
		return err
	}
	secret := p.Secret()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.welcome, r.creds, r.rules, r.proxy = w, creds, rules, p
	r.redact = func(s string) string {
		for _, v := range []string{secret, creds.GatewayToken().Reveal(), creds.MCPToken().Reveal()} {
			if v != "" {
				s = strings.ReplaceAll(s, v, "[redacted]")
			}
		}
		return s
	}
	r.weight = w.Model.DefaultWeight
	if r.weight == "" {
		r.weight = weightNormal
	}
	r.agentPrompt = systemPrompt(w.Agent)
	if w.Session.Summary != nil {
		r.summary = *w.Session.Summary
	}
	return nil
}

// Close stops the engine and the proxy.
func (r *Runner) Close() error {
	r.mu.Lock()
	e, p := r.eng, r.proxy
	r.eng, r.proxy, r.closed = nil, nil, true
	r.mu.Unlock()
	if e != nil {
		e.stop(r.grace())
	}
	if p != nil {
		return p.Close()
	}
	return nil
}

// Wake runs one turn: the wake goes to the engine as a user message, and
// its answer streams out until the engine's result.
func (r *Runner) Wake(ctx context.Context, t *sdk.Turn, w sdk.Wake) error {
	text := describe(w)
	if err := t.ProfileUpdate(r.profile(describeShort(w))); err != nil {
		return err
	}
	r.note("user: " + text)
	return r.finish(ctx, t, w.Task, text)
}

// CanResume says whether turnID was paused for budget and its engine
// session is still known.
func (r *Runner) CanResume(turnID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.paused[turnID]
	return ok && r.sessionID != ""
}

// Resume continues a turn paused for budget; the engine session holds its
// state.
func (r *Runner) Resume(ctx context.Context, t *sdk.Turn) error {
	r.mu.Lock()
	task := r.paused[t.ID()]
	delete(r.paused, t.ID())
	r.mu.Unlock()
	return r.finish(ctx, t, task, budgetBack)
}

// finish converses and, for a task, reports the task done.
func (r *Runner) finish(ctx context.Context, t *sdk.Turn, task *sdk.Task, text string) error {
	final, err := r.converse(ctx, t, text)
	switch {
	case errors.Is(err, sdk.ErrBudgetExhausted):
		r.mu.Lock()
		r.paused[t.ID()] = task
		r.mu.Unlock()
		return err
	case ctx.Err() != nil:
		return nil //nolint:nilerr // a cancelled turn is a requested stop, not a failure
	case err != nil:
		return err
	}
	if task == nil {
		return nil
	}
	summary := sdk.OneLine(final)
	if summary == "" {
		summary = sdk.OneLine("Finished task " + task.Title)
	}
	return t.TaskDone(ctx, sdk.TaskDoneReport{TaskID: task.ID, Outcome: sdk.TaskDone, Summary: summary})
}

// converse sends text, then every message delivered meanwhile, each up to
// the engine's result. Deliveries wait for a result because how an engine
// merges a message sent mid-turn differs between versions.
func (r *Runner) converse(ctx context.Context, t *sdk.Turn, text string) (string, error) {
	for {
		final, err := r.ask(ctx, t, text)
		if err != nil {
			return final, err
		}
		ds := t.Deliveries()
		if len(ds) == 0 {
			return final, nil
		}
		parts := make([]string, 0, len(ds))
		for _, d := range ds {
			who := "someone"
			switch {
			case d.Chat != nil:
				who = d.Chat.From
			case d.Bus != nil:
				who = d.Bus.From
			}
			parts = append(parts, "New message from "+who+" while you work: "+d.Text())
		}
		text = strings.Join(parts, "\n\n")
		r.note("user: " + text)
	}
}

// sink is where an exchange reports. *sdk.Turn is one.
type sink interface {
	Output(text string) error
	Usage(u sdk.Usage) error
	ProfileUpdate(line string) error
	Log(level sdk.LogLevel, message string, fields map[string]string) error
}

// quiet is the sink of the sleep summary: nothing goes to the daemon.
type quiet struct{ log func(string) }

func (quiet) Output(string) error        { return nil }
func (quiet) Usage(sdk.Usage) error      { return nil }
func (quiet) ProfileUpdate(string) error { return nil }
func (q quiet) Log(_ sdk.LogLevel, message string, _ map[string]string) error {
	q.log(message)
	return nil
}

// ask writes one user message and reads the engine's lines up to its
// result. A cancelled ctx interrupts the engine, and kills it when it does
// not stop in time.
func (r *Runner) ask(ctx context.Context, s sink, text string) (string, error) {
	r.exchange.Lock()
	defer r.exchange.Unlock()
	e, err := r.engine()
	if err != nil {
		return "", err
	}
	if err := e.send(userLine(text)); err != nil {
		return "", r.ended(e)
	}
	streamed := false
	for {
		select {
		case <-ctx.Done():
			r.interrupt(e)
			return "", ctx.Err()
		case l, ok := <-e.lines:
			if !ok {
				return "", r.ended(e)
			}
			if l.Type == "result" {
				return r.result(s, l, streamed)
			}
			streamed = r.line(s, e, l) || streamed
		}
	}
}

// line handles one engine line before the result. It reports whether the
// line streamed text out.
func (r *Runner) line(s sink, e *engine, l engineLine) bool {
	switch l.Type {
	case "system":
		switch l.Subtype {
		case "init":
			e.mu.Lock()
			first := !e.initSeen
			e.initSeen = true
			e.mu.Unlock()
			if !first {
				break
			}
			// Feature detection, not version strings: engines before
			// 2.1.205 answer an interrupt without a receipt. The runner
			// waits for the result either way.
			_ = s.Log(sdk.LogDebug, "the engine started", map[string]string{
				"interruptReceipt": fmt.Sprint(slices.Contains(l.Capabilities, "interrupt_receipt_v1"))})
			if !l.machineConnected() {
				_ = s.Log(sdk.LogWarn, "the machine tools are not connected to the engine", nil)
			}
		case "api_retry":
			_ = s.Log(sdk.LogInfo, "the engine retries a model call", map[string]string{
				"attempt": fmt.Sprint(l.Attempt), "status": fmt.Sprint(l.ErrorStatus), "error": sdk.OneLine(r.redact(l.errorText()))})
		}
	case "stream_event":
		if text, ok := l.textDelta(); ok && text != "" {
			_ = s.Output(text)
			return true
		}
	case "assistant":
		for _, name := range l.toolUses() {
			_ = s.ProfileUpdate(r.profile("calling tool " + name))
		}
	}
	return false
}

// result ends one exchange: usage, the final text, or the error it carries.
func (r *Runner) result(s sink, l engineLine, streamed bool) (string, error) {
	r.mu.Lock()
	weight, name := r.weight, r.welcome.Model.Name
	r.mu.Unlock()
	if u := l.Usage; u != nil {
		model := l.model()
		if model == "" {
			model = name
		}
		_ = s.Usage(sdk.Usage{Model: model, Weight: weight, TokensIn: u.InputTokens, TokensOut: u.OutputTokens, CacheReadTokens: u.CacheReadTokens})
	}
	if !l.IsError {
		if !streamed && l.Result != "" {
			_ = s.Output(l.Result)
		}
		r.note("assistant: " + l.Result)
		return l.Result, nil
	}
	switch l.APIErrorStatus {
	case http.StatusPaymentRequired:
		// Only the gateway's budget wall answers 402.
		return "", sdk.ErrBudgetExhausted
	case http.StatusLocked:
		return "", &sdk.CodedError{Code: sdk.CodeModelUnavailable, Message: frozenMessage}
	case http.StatusTooManyRequests:
		return "", &sdk.CodedError{Code: sdk.CodeRateLimited, Message: "The model gateway is limiting calls; try again later"}
	}
	detail := l.Subtype
	if l.APIErrorStatus != 0 {
		detail = fmt.Sprintf("%s, gateway status %d", detail, l.APIErrorStatus)
	}
	return "", &sdk.CodedError{Code: sdk.CodeInternalError, Message: sdk.OneLine("The Claude engine ended the turn with an error (" + r.redact(detail) + ")")}
}

// interrupt stops the engine's turn. Without a result in time the engine is
// killed; the next exchange restarts it with --resume.
func (r *Runner) interrupt(e *engine) {
	if e.send(interruptLine("int_"+newUUID())) == nil {
		deadline := time.After(r.grace())
	wait:
		for {
			select {
			case l, ok := <-e.lines:
				if !ok {
					break wait
				}
				if l.Type == "result" {
					return
				}
			case <-deadline:
				break wait
			}
		}
	}
	r.drop(e)
}

// drop stops e and forgets it, so the next exchange starts a new engine.
func (r *Runner) drop(e *engine) {
	r.mu.Lock()
	if r.eng == e {
		r.eng = nil
	}
	r.mu.Unlock()
	e.stop(0)
}

// ended is the error of an engine that stopped on its own.
func (r *Runner) ended(e *engine) error {
	e.waitExit(r.grace()) // let stderr finish before reading it
	r.drop(e)
	tail := e.stderrLines()
	for _, l := range tail {
		if strings.Contains(l, rootRefusal) {
			return &sdk.CodedError{Code: sdk.CodeModelUnavailable, Message: rootMessage}
		}
	}
	msg := "The Claude engine stopped unexpectedly"
	if len(tail) > 0 {
		msg += ": " + tail[len(tail)-1]
	}
	return &sdk.CodedError{Code: sdk.CodeInternalError, Message: sdk.OneLine(msg)}
}

// engine returns the running engine, starting it when there is none.
func (r *Runner) engine() (*engine, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.eng != nil && !r.eng.exited() {
		return r.eng, nil
	}
	if r.closed || r.proxy == nil {
		return nil, errors.New("the runner is not started")
	}
	if r.cfg.EnginePath == "" {
		return nil, &sdk.CodedError{Code: sdk.CodeModelUnavailable, Message: licenceMessage}
	}
	if _, err := os.Stat(r.cfg.EnginePath); err != nil {
		return nil, &sdk.CodedError{Code: sdk.CodeModelUnavailable, Message: licenceMessage}
	}
	if r.cfg.StateDir == "" {
		return nil, errors.New("the runner has no home folder for the engine's state")
	}
	if r.sessionID == "" {
		r.sessionID, r.resumable = newUUID(), false
	}
	system := r.agentPrompt
	if r.summary != "" {
		system += "\n\nSummary of your earlier work:\n" + r.summary
	}
	l := buildLaunch(launchSpec{
		Weight: r.weight, SystemPrompt: system, ProxyURL: r.proxy.URL(), Secret: r.proxy.Secret(),
		SessionID: r.sessionID, Resume: r.resumable, IsRoot: r.welcome.Agent.IsRoot, Rules: r.rules, StateDir: r.cfg.StateDir,
	}, os.Environ())
	if err := writeFiles(l.Files); err != nil {
		return nil, err
	}
	// The engine works in the agent's home, not in the runner's folder in
	// the image, which a custom agent cannot write.
	home, _ := os.UserHomeDir() // empty keeps the runner's own folder
	e, err := startEngine(r.cfg.EnginePath, home, l, r.redact, r.cfg.Log)
	if err != nil {
		return nil, err
	}
	r.eng, r.resumable = e, true
	return e, nil
}

// note keeps a line of the transcript for the fallback summary.
func (r *Runner) note(line string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.transcript = append(r.transcript, line)
	if len(r.transcript) > maxTranscript {
		r.transcript = r.transcript[len(r.transcript)-maxTranscript:]
	}
}

// Sleep asks the engine for a summary and ends its session: the next wake
// starts a new session with the summary in its system prompt. When the
// summary call fails, for example for budget, the transcript is cut to fit
// instead (rule S1).
func (r *Runner) Sleep(ctx context.Context, req sdk.SleepRequest) (string, error) {
	r.mu.Lock()
	prior, transcript := r.summary, strings.Join(r.transcript, "\n")
	active := r.sessionID != ""
	r.mu.Unlock()
	var summary string
	switch {
	case !active && transcript == "":
		summary = prior
		if summary == "" {
			summary = "No activity yet."
		}
	default:
		ask := fmt.Sprintf("%s in Markdown, in at most %d characters. Keep decisions, open work and facts you need later.", askSummary, req.MaxSummaryChars)
		res, err := r.ask(ctx, quiet{log: r.cfg.Log}, ask)
		summary = strings.TrimSpace(res)
		if err != nil || summary == "" {
			if prior != "" {
				transcript = "Earlier summary: " + prior + "\n\n" + transcript
			}
			summary = cutNote + tail(transcript, req.MaxSummaryChars-utf8.RuneCountInString(cutNote))
		}
	}
	summary = head(summary, req.MaxSummaryChars)
	r.mu.Lock()
	e := r.eng
	r.eng, r.sessionID, r.resumable = nil, "", false
	r.summary, r.transcript = summary, nil
	r.mu.Unlock()
	if e != nil {
		e.stop(r.grace())
	}
	return summary, nil
}

// profile builds the one-line profile: the goal and what the agent does now.
func (r *Runner) profile(action string) string {
	r.mu.Lock()
	goal := strings.Join(r.welcome.Agent.Goals, "; ")
	if goal == "" {
		goal = r.welcome.Agent.Charter
	}
	r.mu.Unlock()
	return sdk.OneLine("Goal: " + goal + ". Now: " + action)
}

func head(s string, maxRunes int) string {
	if utf8.RuneCountInString(s) <= maxRunes {
		return s
	}
	return string([]rune(s)[:max(maxRunes, 0)])
}

// tail keeps the end of s: the newest part of a transcript.
func tail(s string, maxRunes int) string {
	runes := []rune(s)
	maxRunes = max(maxRunes, 0)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[len(runes)-maxRunes:])
}

func systemPrompt(a sdk.Agent) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are %s, an agent that works on this machine.", a.Name)
	if a.Charter != "" {
		b.WriteString("\n\nCharter: " + a.Charter)
	}
	if len(a.Goals) > 0 {
		b.WriteString("\n\nGoals:\n")
		for _, g := range a.Goals {
			b.WriteString("- " + g + "\n")
		}
	}
	b.WriteString("\nYour machine tools are the mcp__machine__ tools. Answer briefly and plainly.")
	return b.String()
}

// describe turns a wake into the text the engine reads.
func describe(w sdk.Wake) string {
	switch w.Reason {
	case sdk.WakeChat:
		return w.Chat.From + " says: " + w.Chat.Text
	case sdk.WakeMessage:
		return fmt.Sprintf("Message from %s, subject %q: %s", w.Message.From, w.Message.Subject, w.Message.Body)
	case sdk.WakeTask:
		return fmt.Sprintf("Task from %s: %s\n%s", w.Task.From, w.Task.Title, w.Task.Body)
	case sdk.WakeEvent:
		data, _ := json.Marshal(w.Event.Data)
		return fmt.Sprintf("Event %s: %s", w.Event.Type, data)
	case sdk.WakeTimer:
		note := ""
		if w.Timer.Note != nil {
			note = " " + *w.Timer.Note
		}
		return "Timer " + w.Timer.Name + " fired." + note
	}
	return "You were woken."
}

// describeShort is the "what I am doing now" part of the profile line.
func describeShort(w sdk.Wake) string {
	switch w.Reason {
	case sdk.WakeChat:
		return "answering " + w.Chat.From
	case sdk.WakeMessage:
		return "reading a message from " + w.Message.From
	case sdk.WakeTask:
		return "working on task " + w.Task.Title
	case sdk.WakeEvent:
		return "handling event " + w.Event.Type
	case sdk.WakeTimer:
		return "handling timer " + w.Timer.Name
	}
	return "starting up"
}
