// SPDX-License-Identifier: MIT

package sdk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/SDK-E/agentsone/runners/sdk/protocol"
)

// maxChunkBytes is the largest text one output chunk carries. A chunk of this
// size is far below the line limit even when JSON escaping grows it six times.
const maxChunkBytes = 64 * 1024

// CodedError is an error with a protocol error code. A handler returns one to
// choose the code of the error notification the SDK sends when a turn fails,
// for example CodeModelUnavailable.
type CodedError struct {
	Code    int
	Message string
}

// Error implements error.
func (e *CodedError) Error() string { return e.Message }

// Wire shapes of runner-to-daemon messages; field order follows PROTOCOL.md.
type (
	statusParams struct {
		State        string `json:"state"`
		TurnID       string `json:"turnId,omitempty"`
		PausedTurnID string `json:"pausedTurnId,omitempty"`
		Detail       string `json:"detail,omitempty"`
	}
	outputParams struct {
		TurnID string  `json:"turnId"`
		ChatID *string `json:"chatId,omitempty"`
		Chunk  int     `json:"chunk"`
		Text   string  `json:"text"`
		Final  bool    `json:"final"`
	}
	profileParams struct {
		TurnID string `json:"turnId"`
		Line   string `json:"line"`
	}
	usageParams struct {
		TurnID          string `json:"turnId"`
		Model           string `json:"model"`
		Weight          string `json:"weight"`
		TokensIn        int    `json:"tokensIn"`
		TokensOut       int    `json:"tokensOut"`
		CacheReadTokens *int   `json:"cacheReadTokens,omitempty"`
		CostMicroUsd    int    `json:"costMicroUsd"`
	}
	busSendParams struct {
		TurnID        string `json:"turnId"`
		To            string `json:"to"`
		Subject       string `json:"subject"`
		Body          string `json:"body"`
		ReplyTo       string `json:"replyTo,omitempty"`
		CorrelationID string `json:"correlationId,omitempty"`
	}
	taskDoneParams struct {
		TurnID  string `json:"turnId"`
		TaskID  string `json:"taskId"`
		Outcome string `json:"outcome"`
		Summary string `json:"summary"`
		Details string `json:"details,omitempty"`
	}
	logParams struct {
		Level   string            `json:"level"`
		Message string            `json:"message"`
		TurnID  string            `json:"turnId,omitempty"`
		Fields  map[string]string `json:"fields,omitempty"`
	}
	errorParams struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		TurnID  string `json:"turnId,omitempty"`
		Fatal   bool   `json:"fatal"`
	}
)

// Turn is one unit of work. Handlers talk to the daemon through it; it stamps
// the turn id on every message and keeps the turn rules.
type Turn struct {
	id     string
	chatID *string
	s      *session

	mu          sync.Mutex
	ctx         context.Context
	cancel      context.CancelFunc
	cancelled   bool
	resumed     bool
	chunk       int
	outputDone  bool
	profileSent bool
	inbox       []Delivery
	signal      chan struct{}
}

func newTurn(s *session, id string, chatID *string) *Turn {
	return &Turn{id: id, chatID: chatID, s: s, signal: make(chan struct{}, 1)}
}

// start gives the turn a fresh context under parent. It runs for the first
// start and again for a resume.
func (t *Turn) start(parent context.Context) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.ctx, t.cancel = context.WithCancel(parent)
}

// ID is the turn id chosen by the daemon.
func (t *Turn) ID() string { return t.id }

// Resumed reports whether this run of the turn continues one paused for budget.
func (t *Turn) Resumed() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.resumed
}

// Context ends when the turn is cancelled or the runner shuts down.
func (t *Turn) Context() context.Context {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.ctx
}

func (t *Turn) markCancelled() {
	t.mu.Lock()
	t.cancelled = true
	cancel := t.cancel
	t.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (t *Turn) wasCancelled() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.cancelled
}

func (t *Turn) push(d Delivery) {
	t.mu.Lock()
	t.inbox = append(t.inbox, d)
	t.mu.Unlock()
	select {
	case t.signal <- struct{}{}:
	default:
	}
}

// Delivered fires when messages were delivered into the turn. Read them with
// Deliveries.
func (t *Turn) Delivered() <-chan struct{} { return t.signal }

// Deliveries returns and clears the messages delivered since the last call,
// in arrival order.
func (t *Turn) Deliveries() []Delivery {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := t.inbox
	t.inbox = nil
	return out
}

// Output sends text as one or more non-final chunks (rule T5). Text longer
// than the chunk limit is split on character boundaries.
func (t *Turn) Output(text string) error { return t.output(text, false) }

// FinishOutput sends text as the final chunk. Nothing may follow it in this
// turn. A turn that sent output and ends without a final chunk gets an empty
// final chunk from the SDK.
func (t *Turn) FinishOutput(text string) error { return t.output(text, true) }

func (t *Turn) output(text string, final bool) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.outputDone {
		return ErrOutputClosed
	}
	parts := splitText(text, maxChunkBytes)
	for i, part := range parts {
		last := i == len(parts)-1
		p := outputParams{TurnID: t.id, ChatID: t.chatID, Chunk: t.chunk, Text: part, Final: final && last}
		if err := t.s.conn.notify(protocol.MethodOutput, p); err != nil {
			return err
		}
		t.chunk++
	}
	if final {
		t.outputDone = true
	}
	return nil
}

// closeOutput ends output that was started but not finished (rule T5).
func (t *Turn) closeOutput() error {
	t.mu.Lock()
	started := t.chunk > 0
	done := t.outputDone
	t.mu.Unlock()
	if !started || done {
		return nil
	}
	return t.FinishOutput("")
}

// splitText cuts s into pieces of at most max bytes without splitting a
// character. Empty text gives one empty piece.
func splitText(s string, max int) []string {
	if len(s) <= max {
		return []string{s}
	}
	var parts []string
	for len(s) > max {
		cut := max
		for cut > 0 && !utf8.RuneStart(s[cut]) {
			cut--
		}
		parts = append(parts, s[:cut])
		s = s[cut:]
	}
	return append(parts, s)
}

// ProfileUpdate sends the agent's one-line profile: its goal and what it is
// doing now. It must be 1 to 255 characters with no line break. Every turn
// needs at least one (rule T3).
func (t *Turn) ProfileUpdate(line string) error {
	if err := ValidateOneLine(line); err != nil {
		return err
	}
	if err := t.s.conn.notify(protocol.MethodProfileUpdate, profileParams{TurnID: t.id, Line: line}); err != nil {
		return err
	}
	t.mu.Lock()
	t.profileSent = true
	t.mu.Unlock()
	t.s.setOneLine(line)
	return nil
}

// Usage reports model tokens and a cost estimate for this turn (rule T6).
func (t *Turn) Usage(u Usage) error {
	if u.TokensIn < 0 || u.TokensOut < 0 || u.CostMicroUsd < 0 {
		return errors.New("sdk: usage numbers must not be negative")
	}
	return t.s.conn.notify(protocol.MethodUsage, usageParams{
		TurnID: t.id, Model: u.Model, Weight: u.Weight, TokensIn: u.TokensIn, TokensOut: u.TokensOut,
		CacheReadTokens: u.CacheReadTokens, CostMicroUsd: u.CostMicroUsd,
	})
}

// BusSend sends a message to another agent and returns its message id. The
// daemon's refusals come back as *RPCError, for example CodeUnknownRecipient.
func (t *Turn) BusSend(ctx context.Context, m BusSend) (string, error) {
	raw, err := t.s.conn.call(ctx, protocol.MethodBusSend, busSendParams{
		TurnID: t.id, To: m.To, Subject: m.Subject, Body: m.Body, ReplyTo: m.ReplyTo, CorrelationID: m.CorrelationID,
	})
	if err != nil {
		return "", err
	}
	var res struct {
		MessageID string `json:"messageId"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return "", fmt.Errorf("read bus.send result: %w", err)
	}
	return res.MessageID, nil
}

// TaskDone reports that work on a task ended.
func (t *Turn) TaskDone(ctx context.Context, r TaskDoneReport) error {
	_, err := t.s.conn.call(ctx, protocol.MethodTaskDone, taskDoneParams{
		TurnID: t.id, TaskID: r.TaskID, Outcome: string(r.Outcome), Summary: r.Summary, Details: r.Details,
	})
	return err
}

// Log sends a diagnostic line for the agent's journal. Never put tokens in it.
func (t *Turn) Log(level LogLevel, message string, fields map[string]string) error {
	return t.s.conn.notify(protocol.MethodLog, logParams{Level: string(level), Message: message, TurnID: t.id, Fields: fields})
}

// Error reports a failure outside a request, such as a model call.
func (t *Turn) Error(code int, message string, fatal bool) error {
	return t.s.conn.notify(protocol.MethodError, errorParams{Code: code, Message: message, TurnID: t.id, Fatal: fatal})
}

// ValidateOneLine checks the profile line rule: 1 to 255 characters, no line
// break.
func ValidateOneLine(line string) error {
	n := utf8.RuneCountInString(line)
	if n < 1 || n > MaxOneLine || strings.ContainsAny(line, "\r\n") {
		return ErrInvalidOneLine
	}
	return nil
}

// OneLine makes any text fit the one-line rule: line breaks become spaces,
// runs of spaces collapse, and text over 255 characters is cut with an
// ellipsis. It returns "" for blank text.
func OneLine(text string) string {
	line := strings.Join(strings.Fields(text), " ")
	if utf8.RuneCountInString(line) <= MaxOneLine {
		return line
	}
	runes := []rune(line)
	return strings.TrimRight(string(runes[:MaxOneLine-1]), " ") + "…"
}
