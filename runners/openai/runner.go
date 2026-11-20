// SPDX-License-Identifier: MIT

// Package openai is a runner that talks to an OpenAI-compatible chat
// completions gateway. It streams the answer, calls the agent's tools through
// the machine MCP endpoint, keeps the profile line current, compacts its
// session when it is told to sleep, and pauses a turn when the gateway says
// the budget is spent.
//
// The gateway URL and token come from the welcome. Nothing is hard-coded.
package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/SDK-E/agentsone/runners/sdk"
)

// Version is the runner version sent in hello. 1.1.0 renews its tokens on
// protocol 1.1 without a restart.
const Version = "1.1.0"

// DefaultMaxSteps bounds the model calls of one turn, so a model that keeps
// calling tools cannot spend the budget in a loop.
const DefaultMaxSteps = 12

const (
	weightQuick   = "quick"
	weightNormal  = "normal"
	statusNoTools = "continuing without tools"
)

// Options returns the SDK options of the runner.
func Options() sdk.Options {
	return sdk.Options{
		Runner:       sdk.Info{Name: "openai", Version: Version},
		Capabilities: sdk.Capabilities{Streaming: true, Cancel: true, Compaction: true, Resume: true},
	}
}

// Config holds what tests and the main package may change.
type Config struct {
	// HTTP is the client for the gateway and the MCP endpoint. Default: a
	// client with no timeout, because answers stream; turn contexts end calls.
	HTTP *http.Client
	// Dial opens the tool box. Default: DialMCP.
	Dial Dialer
	// MaxSteps bounds model calls per turn. Default: DefaultMaxSteps.
	MaxSteps int
}

// Runner is the handler of the OpenAI-compatible runner.
type Runner struct {
	cfg Config

	mu      sync.Mutex
	welcome sdk.Welcome
	creds   *sdk.Credentials
	gw      *Gateway
	weight  string
	system  string
	summary string
	history []Message
	paused  map[string]pausedTurn
	tools   ToolBox
	defs    []ToolDef
}

type pausedTurn struct{ task *sdk.Task }

var (
	_ sdk.Handler = (*Runner)(nil)
	_ sdk.Resumer = (*Runner)(nil)
)

// New returns a runner. The zero Config is fine.
func New(cfg Config) *Runner {
	if cfg.HTTP == nil {
		cfg.HTTP = &http.Client{}
	}
	if cfg.Dial == nil {
		cfg.Dial = DialMCP
	}
	if cfg.MaxSteps <= 0 {
		cfg.MaxSteps = DefaultMaxSteps
	}
	return &Runner{cfg: cfg, paused: map[string]pausedTurn{}}
}

// Start reads the gateway and the session summary from the welcome.
func (r *Runner) Start(_ context.Context, w sdk.Welcome) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.welcome = w
	r.weight = w.Model.DefaultWeight
	if r.weight == "" {
		r.weight = weightNormal
	}
	// The tokens come from the credentials, which the SDK renews in place, so
	// the runner never restarts for a new token.
	r.creds = w.Credentials
	r.gw = &Gateway{BaseURL: w.Model.BaseURL, Token: w.Credentials.GatewayToken, Model: w.Model.Name, HTTP: r.cfg.HTTP}
	r.system = systemPrompt(w.Agent)
	if w.Session.Summary != nil {
		r.summary = *w.Session.Summary
	}
	return nil
}

// Close ends the session with the tool server.
func (r *Runner) Close() error {
	r.mu.Lock()
	box := r.tools
	r.tools, r.defs = nil, nil
	r.mu.Unlock()
	if box == nil {
		return nil
	}
	return box.Close()
}

// Wake runs one turn: the wake text goes to the model, tool calls are run, and
// the answer streams out until the model stops asking for tools.
func (r *Runner) Wake(ctx context.Context, t *sdk.Turn, w sdk.Wake) error {
	r.add(Message{Role: "user", Content: describe(w)})
	if err := t.ProfileUpdate(r.profile(describeShort(w))); err != nil {
		return err
	}
	return r.finish(ctx, t, w.Task)
}

// CanResume says whether turnID was paused by this runner.
func (r *Runner) CanResume(turnID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.paused[turnID]
	return ok
}

// Resume continues a turn that was paused for budget. The history holds the
// state, so the model is called again with it.
func (r *Runner) Resume(ctx context.Context, t *sdk.Turn) error {
	r.mu.Lock()
	p := r.paused[t.ID()]
	delete(r.paused, t.ID())
	r.mu.Unlock()
	return r.finish(ctx, t, p.task)
}

// finish runs the loop and, for a task, reports the task done.
func (r *Runner) finish(ctx context.Context, t *sdk.Turn, task *sdk.Task) error {
	final, err := r.loop(ctx, t)
	switch {
	case errors.Is(err, sdk.ErrBudgetExhausted):
		r.mu.Lock()
		r.paused[t.ID()] = pausedTurn{task: task}
		r.mu.Unlock()
		return err
	case errors.Is(err, context.Canceled), ctx.Err() != nil:
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

// loop calls the model until it answers without tool calls.
func (r *Runner) loop(ctx context.Context, t *sdk.Turn) (string, error) {
	for step := 0; step < r.cfg.MaxSteps; step++ {
		r.absorb(t)
		defs := r.toolDefs(ctx, t)
		comp, err := r.complete(ctx, t, defs)
		if err != nil {
			return "", err
		}
		r.add(Message{Role: "assistant", Content: comp.Content, ToolCalls: comp.ToolCalls})
		if len(comp.ToolCalls) == 0 {
			return comp.Content, nil
		}
		for _, call := range comp.ToolCalls {
			r.add(Message{Role: "tool", ToolCallID: call.ID, Content: r.invoke(ctx, t, call)})
		}
	}
	msg := fmt.Sprintf("\n[Stopped after %d model calls.]", r.cfg.MaxSteps)
	r.add(Message{Role: "assistant", Content: msg})
	return msg, t.Output(msg)
}

func (r *Runner) complete(ctx context.Context, t *sdk.Turn, defs []ToolDef) (Completion, error) {
	r.mu.Lock()
	gw, weight := r.gw, r.weight
	r.mu.Unlock()
	req := ChatRequest{Weight: weight, Messages: r.messages(), Tools: defs}
	comp, err := gw.Complete(ctx, req, t.Output)
	if err != nil {
		return comp, err
	}
	var cached *int
	if d := comp.Usage.PromptTokensDetails; d != nil {
		cached = &d.CachedTokens
	}
	// The gateway does not price calls; the daemon's meter does.
	uerr := t.Usage(sdk.Usage{Model: comp.Model, Weight: weight, TokensIn: comp.Usage.PromptTokens, TokensOut: comp.Usage.CompletionTokens, CacheReadTokens: cached})
	return comp, uerr
}

// invoke runs one tool call and returns the text the model reads as its
// result. A failure is text too, so the model can react to it.
func (r *Runner) invoke(ctx context.Context, t *sdk.Turn, call ToolCall) string {
	if ctx.Err() != nil {
		return "error: the turn was cancelled"
	}
	r.mu.Lock()
	box := r.tools
	r.mu.Unlock()
	if box == nil {
		return "error: no tools are available"
	}
	args := json.RawMessage(call.Function.Arguments)
	if strings.TrimSpace(call.Function.Arguments) == "" {
		args = json.RawMessage(`{}`)
	}
	if !json.Valid(args) {
		return "error: the arguments are not valid JSON"
	}
	_ = t.ProfileUpdate(r.profile("calling tool " + call.Function.Name)) // a long call may fail the profile line only
	out, err := box.Call(ctx, call.Function.Name, args)
	if err != nil {
		_ = t.Log(sdk.LogWarn, "tool call failed", map[string]string{"tool": call.Function.Name})
		return "error: " + err.Error()
	}
	return out
}

// toolDefs lists the tools, connecting on the first turn. A tool server that
// cannot be reached leaves the turn without tools, with a warning in the log.
func (r *Runner) toolDefs(ctx context.Context, t *sdk.Turn) []ToolDef {
	r.mu.Lock()
	if r.tools != nil {
		defs := r.defs
		r.mu.Unlock()
		return defs
	}
	mcp, creds := r.welcome.MCP, r.creds
	r.mu.Unlock()
	box, err := r.cfg.Dial(ctx, mcp, creds.MCPToken, r.cfg.HTTP)
	if err == nil {
		var defs []ToolDef
		if defs, err = box.Tools(ctx); err == nil {
			r.mu.Lock()
			r.tools, r.defs = box, defs
			r.mu.Unlock()
			return defs
		}
		_ = box.Close()
	}
	if ctx.Err() == nil {
		_ = t.Log(sdk.LogWarn, "tools unavailable, "+statusNoTools, map[string]string{"error": sdk.OneLine(err.Error())})
	}
	return nil
}

// absorb moves messages delivered into the turn into the history.
func (r *Runner) absorb(t *sdk.Turn) {
	for _, d := range t.Deliveries() {
		who := "someone"
		switch {
		case d.Chat != nil:
			who = d.Chat.From
		case d.Bus != nil:
			who = d.Bus.From
		}
		r.add(Message{Role: "user", Content: "New message from " + who + " while you work: " + d.Text()})
	}
}

func (r *Runner) add(m Message) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.history = append(r.history, m)
}

// messages is the system prompt followed by the history.
func (r *Runner) messages() []Message {
	r.mu.Lock()
	defer r.mu.Unlock()
	system := r.system
	if r.summary != "" {
		system += "\n\nSummary of your earlier work:\n" + r.summary
	}
	return append([]Message{{Role: "system", Content: system}}, r.history...)
}

// profile builds the one-line profile: the goal and what the agent does now.
func (r *Runner) profile(action string) string {
	r.mu.Lock()
	goal := strings.Join(r.welcome.Agent.Goals, "; ")
	r.mu.Unlock()
	if goal == "" {
		goal = r.welcome.Agent.Charter
	}
	return sdk.OneLine("Goal: " + goal + ". Now: " + action)
}

// Sleep compacts the session into a summary with one quick model call. When
// the call fails, for example because the budget is spent, it cuts the
// transcript down to fit instead (rule S1).
func (r *Runner) Sleep(ctx context.Context, req sdk.SleepRequest) (string, error) {
	r.mu.Lock()
	hist := append([]Message(nil), r.history...)
	prior, gw := r.summary, r.gw
	system := r.system
	r.mu.Unlock()
	var summary string
	switch {
	case len(hist) == 0:
		summary = prior
		if summary == "" {
			summary = "No activity yet."
		}
	default:
		transcript := render(hist)
		ask := fmt.Sprintf("Summarise this session in Markdown, in at most %d characters. Keep decisions, open work and facts you need later.\n\n%s", req.MaxSummaryChars, withPrior(prior, transcript))
		comp, err := gw.Complete(ctx, ChatRequest{Weight: weightQuick, Messages: []Message{{Role: "system", Content: system}, {Role: "user", Content: ask}}}, func(string) error { return nil })
		summary = strings.TrimSpace(comp.Content)
		if err != nil || summary == "" {
			const note = "Transcript cut to fit.\n"
			summary = note + tail(withPrior(prior, transcript), req.MaxSummaryChars-utf8.RuneCountInString(note))
		}
	}
	summary = head(summary, req.MaxSummaryChars)
	r.mu.Lock()
	r.summary, r.history = summary, nil
	r.mu.Unlock()
	return summary, nil
}

func withPrior(prior, transcript string) string {
	if prior == "" {
		return transcript
	}
	return "Earlier summary: " + prior + "\n\n" + transcript
}

// render shows the history as plain text.
func render(msgs []Message) string {
	var b strings.Builder
	for _, m := range msgs {
		switch {
		case len(m.ToolCalls) > 0:
			for _, c := range m.ToolCalls {
				fmt.Fprintf(&b, "assistant called %s(%s)\n", c.Function.Name, c.Function.Arguments)
			}
		default:
			fmt.Fprintf(&b, "%s: %s\n", m.Role, m.Content)
		}
	}
	return b.String()
}

func head(s string, maxRunes int) string {
	if utf8.RuneCountInString(s) <= maxRunes {
		return s
	}
	return string([]rune(s)[:maxRunes])
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
	b.WriteString("\nUse your tools when they help. Answer briefly and plainly.")
	return b.String()
}

// describe turns a wake into the text the model reads.
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
