// SPDX-License-Identifier: MIT

// Package echo is a runner with no model. It answers every wake by echoing the
// text back, so the daemon, the chat and the bus can be tested end to end with
// output that never changes. It also serves as the smallest example of a
// runner built on the SDK.
//
// Besides plain text it understands three test commands at the start of a chat
// or message body:
//
//	/hold      keep the turn open until it is cancelled, and echo deliveries
//	/budget    pause the turn as if the budget ran out; resume it on wake
//	/long N    answer with N bytes of text
package echo

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/SDK-E/agentsone/runners/sdk"
)

// Version is the runner version sent in hello.
const Version = "1.0.0"

// Command texts the runner reacts to.
const (
	CmdHold   = "/hold"
	CmdBudget = "/budget"
	CmdLong   = "/long"
)

// maxLongBytes bounds /long so a typo cannot fill the disk.
const maxLongBytes = 16 << 20

// Options returns the SDK options of the echo runner.
func Options() sdk.Options {
	return sdk.Options{
		Runner:       sdk.Info{Name: "echo", Version: Version},
		Capabilities: sdk.Capabilities{Streaming: true, Cancel: true, Compaction: true, Resume: true},
	}
}

// Runner is the echo handler.
type Runner struct {
	mu      sync.Mutex
	summary string // session summary from the welcome, until the first turn used it
	last    string // last text echoed, for the sleep summary
	turns   int
	paused  map[string]struct{}
}

// New returns an echo runner.
func New() *Runner {
	return &Runner{paused: map[string]struct{}{}}
}

var (
	_ sdk.Handler = (*Runner)(nil)
	_ sdk.Resumer = (*Runner)(nil)
)

// Start keeps the session summary: the first turn repeats it, which is how the
// echo shows that a session started from a summary (rule S2).
func (r *Runner) Start(_ context.Context, w sdk.Welcome) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if w.Session.Summary != nil {
		r.summary = *w.Session.Summary
	}
	return nil
}

// Wake echoes the wake text.
func (r *Runner) Wake(ctx context.Context, t *sdk.Turn, w sdk.Wake) error {
	text := w.Text()
	if err := t.ProfileUpdate(profile(w, text)); err != nil {
		return err
	}
	if err := r.reportUsage(t); err != nil {
		return err
	}
	r.note(text)
	switch {
	case strings.HasPrefix(text, CmdBudget):
		r.pause(t.ID())
		return sdk.ErrBudgetExhausted
	case strings.HasPrefix(text, CmdHold):
		return r.hold(ctx, t)
	case strings.HasPrefix(text, CmdLong):
		return t.FinishOutput(long(text))
	}
	if err := t.FinishOutput(r.reply(w, text)); err != nil {
		return err
	}
	if w.Task != nil {
		return t.TaskDone(ctx, sdk.TaskDoneReport{TaskID: w.Task.ID, Outcome: sdk.TaskDone, Summary: sdk.OneLine("Echoed task: " + w.Task.Title)})
	}
	return nil
}

// CanResume says whether turnID was paused by this runner.
func (r *Runner) CanResume(turnID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.paused[turnID]
	return ok
}

// Resume finishes a turn that was paused for budget.
func (r *Runner) Resume(_ context.Context, t *sdk.Turn) error {
	r.mu.Lock()
	delete(r.paused, t.ID())
	r.mu.Unlock()
	return t.FinishOutput("echo: resumed")
}

// Sleep summarises the session in one sentence.
func (r *Runner) Sleep(_ context.Context, req sdk.SleepRequest) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	summary := fmt.Sprintf("Echoed %d messages. Last: %s", r.turns, r.last)
	if r.summary != "" {
		summary = r.summary + " " + summary
	}
	return truncate(summary, req.MaxSummaryChars), nil
}

func (r *Runner) note(text string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.turns++
	r.last = sdk.OneLine(text)
}

func (r *Runner) pause(turnID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.paused[turnID] = struct{}{}
}

// reply is the echo of one wake. The first reply after a start from a summary
// begins with that summary.
func (r *Runner) reply(w sdk.Wake, text string) string {
	r.mu.Lock()
	summary := r.summary
	r.summary = ""
	r.mu.Unlock()
	var b strings.Builder
	if summary != "" {
		b.WriteString("Session summary: " + summary + "\n")
	}
	switch w.Reason {
	case sdk.WakeEvent:
		b.WriteString("echo: event " + w.Event.Type)
	case sdk.WakeTimer:
		b.WriteString("echo: timer " + w.Timer.Name)
	case sdk.WakeTask:
		b.WriteString("echo: task " + w.Task.Title + ": " + text)
	default:
		b.WriteString("echo: " + text)
	}
	return b.String()
}

func (r *Runner) reportUsage(t *sdk.Turn) error {
	return t.Usage(sdk.Usage{Model: "echo", Weight: "quick"})
}

// hold keeps the turn open and echoes what is delivered into it, until the
// turn is cancelled.
func (r *Runner) hold(ctx context.Context, t *sdk.Turn) error {
	if err := t.Output("echo: holding"); err != nil {
		return err
	}
	done := 0
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.Delivered():
			all := t.Deliveries()
			for _, d := range all[done:] {
				if err := t.Output("\necho: " + d.Text()); err != nil {
					return err
				}
			}
			done = len(all)
		}
	}
}

func profile(w sdk.Wake, text string) string {
	switch w.Reason {
	case sdk.WakeEvent:
		return sdk.OneLine("Goal: echo what I am told. Now: echoing event " + w.Event.Type)
	case sdk.WakeTimer:
		return sdk.OneLine("Goal: echo what I am told. Now: echoing timer " + w.Timer.Name)
	}
	return sdk.OneLine("Goal: echo what I am told. Now: echoing " + text)
}

// long builds the answer to "/long N": N bytes of text.
func long(text string) string {
	n, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(text, CmdLong)))
	if err != nil || n < 0 {
		return "echo: usage /long N"
	}
	n = min(n, maxLongBytes)
	return strings.Repeat("echo ", n/5+1)[:n]
}

func truncate(s string, maxRunes int) string {
	if utf8.RuneCountInString(s) <= maxRunes {
		return s
	}
	return string([]rune(s)[:maxRunes])
}
