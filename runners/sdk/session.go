// SPDX-License-Identifier: MIT

package sdk

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/SDK-E/agentsone/runners/sdk/protocol"
)

type queued struct {
	wake   Wake
	resume bool
}

type shutdownRequest struct{ grace time.Duration }

// session is one handshaken connection: the dispatcher for daemon requests,
// the turn worker and the heartbeat.
type session struct {
	conn    *conn
	handler Handler
	opts    Options
	clock   Clock
	welcome Welcome
	started time.Time

	// creds are the tokens handed to the runner in Welcome.Credentials.
	creds *Credentials
	// handshook is closed once the welcome is read and creds hold its tokens;
	// renews is set before that and says whether the connection negotiated
	// credentials.update (1.1 or later).
	handshook chan struct{}
	renews    bool

	mu       sync.Mutex
	ready    bool
	current  *Turn
	paused   *Turn
	pausedID string
	queue    []queued
	oneLine  string

	wakeSignal chan struct{}
	shutdown   chan shutdownRequest
	workerDone chan struct{}
	turnCtx    context.Context
}

func (s *session) setOneLine(line string) {
	s.mu.Lock()
	s.oneLine = line
	s.mu.Unlock()
}

func (s *session) currentOneLine() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.oneLine
}

func (s *session) status(p statusParams) {
	_ = s.conn.notify(protocol.MethodStatus, p)
}

func (s *session) isReady() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ready
}

// readLoop reads and dispatches lines until the connection ends.
func (s *session) readLoop() error {
	welcomed := false // the hello answer, the first response, has been read
	for {
		line, err := s.conn.lines.next()
		if errors.Is(err, errLineTooLong) {
			_ = s.conn.respondError(nil, CodeLineTooLarge, "line too large", nil)
			continue
		}
		if err != nil {
			return ErrDaemonClosed
		}
		in, prob := classify(line)
		if prob != nil {
			_ = s.conn.respondError(prob.id, prob.code, prob.message, nil)
			continue
		}
		switch in.kind {
		case kindResponse:
			s.conn.deliverResponse(in.id, response{result: in.result, err: in.err})
			welcomed = true
		case kindRequest:
			s.handleRequest(in)
		case kindNotification:
			// credentials.update is the daemon's only notification. Before
			// the welcome it is out of order; JSON-RPC says to ignore
			// notifications a receiver does not handle.
			if in.method == protocol.MethodCredentialsUpdate && welcomed {
				s.onCredentials(in.params)
			}
		}
	}
}

// onCredentials swaps the tokens on a valid credentials.update (rule K2). It
// sends no response. A connection that negotiated 1.0 ignores it, as in 1.0
// (K3). A bad update keeps the current tokens and is reported in one log line
// that holds no token text.
func (s *session) onCredentials(params json.RawMessage) {
	// The update may be read while the handshake still reads the welcome;
	// wait for it, so the welcome's tokens never overwrite a newer update.
	select {
	case <-s.handshook:
	case <-s.conn.done:
		return
	}
	if !s.renews {
		return
	}
	gateway, mcp, err := parseCredentials(params, s.clock.Now())
	if err != nil {
		_ = s.conn.notify(protocol.MethodLog, logParams{Level: string(LogWarn), Message: "credentials.update ignored: " + err.Error()})
		return
	}
	s.creds.set(gateway, mcp)
}

func (s *session) reply(id int, result any, err error) {
	switch {
	case err == nil:
		_ = s.conn.respond(id, result)
	case errors.Is(err, errParams):
		_ = s.conn.respondError(&id, CodeInvalidParams, err.Error(), nil)
	default:
		var rpc *RPCError
		if errors.As(err, &rpc) {
			_ = s.conn.respondError(&id, rpc.Code, rpc.Message, nil)
			return
		}
		_ = s.conn.respondError(&id, CodeInternalError, "internal error", nil)
	}
}

func (s *session) handleRequest(in inbound) {
	id := in.id
	if !s.isReady() {
		_ = s.conn.respondError(&id, CodeOutOfOrder, "message before the handshake ended", nil)
		return
	}
	switch in.method {
	case protocol.MethodWake:
		s.onWake(id, in.params)
	case protocol.MethodDeliver:
		s.onDeliver(id, in.params)
	case protocol.MethodCancel:
		s.onCancel(id, in.params)
	case protocol.MethodSleep:
		req, err := parseSleep(in.params)
		if err != nil {
			s.reply(id, nil, err)
			return
		}
		go s.onSleep(id, req)
	case protocol.MethodShutdown:
		p, err := parseShutdown(in.params)
		if err != nil {
			s.reply(id, nil, err)
			return
		}
		_ = s.conn.respond(id, empty{})
		select {
		case s.shutdown <- shutdownRequest{grace: time.Duration(p.graceMs) * time.Millisecond}:
		default:
		}
	default:
		_ = s.conn.respondError(&id, CodeMethodNotFound, "method not found: "+in.method, nil)
	}
}

func (s *session) onWake(id int, params json.RawMessage) {
	w, err := parseWake(params)
	if err != nil {
		s.reply(id, nil, err)
		return
	}
	resume := w.Reason == WakeEvent && w.Event != nil && w.Event.Type == BudgetRestoredEvent
	if resume && !s.canResume(w.TurnID) {
		_ = s.conn.respondError(&id, CodeResumeUnavailable, "Resume unavailable", nil)
		return
	}
	// Acknowledge first: nothing about the turn may precede the answer (T1).
	_ = s.conn.respond(id, empty{})
	s.mu.Lock()
	s.queue = append(s.queue, queued{wake: w, resume: resume})
	s.mu.Unlock()
	select {
	case s.wakeSignal <- struct{}{}:
	default:
	}
}

func (s *session) canResume(turnID string) bool {
	r, ok := s.handler.(Resumer)
	if !ok || !s.opts.Capabilities.Resume {
		return false
	}
	s.mu.Lock()
	known := (s.paused != nil && s.paused.id == turnID) || s.pausedID == turnID
	s.mu.Unlock()
	return known && r.CanResume(turnID)
}

func (s *session) onDeliver(id int, params json.RawMessage) {
	d, err := parseDeliver(params)
	if err != nil {
		s.reply(id, nil, err)
		return
	}
	s.mu.Lock()
	cur := s.current
	s.mu.Unlock()
	if cur == nil || cur.id != d.turnID {
		_ = s.conn.respondError(&id, CodeUnknownTurn, "Unknown turn", map[string]string{"turnId": d.turnID})
		return
	}
	// Answer before the turn sees the message, so the response always comes
	// before anything the turn sends in reaction to it.
	_ = s.conn.respond(id, empty{})
	cur.push(d.delivery)
}

func (s *session) onCancel(id int, params json.RawMessage) {
	c, err := parseCancel(params)
	if err != nil {
		s.reply(id, nil, err)
		return
	}
	_ = s.conn.respond(id, empty{}) // also for a turn that already ended (C1)
	s.mu.Lock()
	cur := s.current
	dropped := false
	for i, q := range s.queue {
		if q.wake.TurnID == c.turnID {
			s.queue = append(s.queue[:i], s.queue[i+1:]...)
			dropped = true
			break
		}
	}
	if s.paused != nil && s.paused.id == c.turnID {
		s.paused = nil
	}
	if s.pausedID == c.turnID {
		s.pausedID = ""
	}
	s.mu.Unlock()
	if cur != nil && cur.id == c.turnID {
		cur.markCancelled()
	}
	if dropped {
		// A turn that never started still ends with status idle (C2).
		s.status(statusParams{State: "idle", TurnID: c.turnID, Detail: "Cancelled"})
	}
}

func (s *session) onSleep(id int, req SleepRequest) {
	summary, err := s.handler.Sleep(s.turnCtx, req)
	if err != nil {
		_ = s.conn.respondError(&id, CodeInternalError, "sleep failed", nil)
		return
	}
	if utf8.RuneCountInString(summary) > req.MaxSummaryChars {
		summary = string([]rune(summary)[:req.MaxSummaryChars])
	}
	_ = s.conn.respond(id, struct {
		Summary string `json:"summary"`
	}{summary})
	s.status(statusParams{State: "sleeping"})
}

func (s *session) setCurrent(t *Turn) {
	s.mu.Lock()
	s.current = t
	s.mu.Unlock()
}

func (s *session) nextQueued(ctx context.Context) (queued, bool) {
	for {
		s.mu.Lock()
		if len(s.queue) > 0 {
			q := s.queue[0]
			s.queue = s.queue[1:]
			s.mu.Unlock()
			return q, true
		}
		s.mu.Unlock()
		select {
		case <-s.wakeSignal:
		case <-ctx.Done():
			return queued{}, false
		}
	}
}

// worker runs the queued turns one at a time (rule T7).
func (s *session) worker(ctx context.Context) {
	defer close(s.workerDone)
	for {
		q, ok := s.nextQueued(ctx)
		if !ok {
			return
		}
		s.runTurn(ctx, q)
	}
}

func (s *session) runTurn(ctx context.Context, q queued) {
	t := s.turnFor(q)
	t.start(ctx)
	s.setCurrent(t)
	s.status(statusParams{State: "working", TurnID: t.id})
	var err error
	if q.resume {
		err = s.handler.(Resumer).Resume(t.Context(), t) //nolint:forcetypeassert // checked in canResume
	} else {
		err = s.handler.Wake(t.Context(), t, q.wake)
	}
	s.finishTurn(t, err)
}

func (s *session) turnFor(q queued) *Turn {
	s.mu.Lock()
	defer s.mu.Unlock()
	if q.resume {
		t := s.paused
		if t == nil || t.id != q.wake.TurnID {
			t = newTurn(s, q.wake.TurnID, nil)
		}
		t.mu.Lock()
		t.resumed = true
		t.cancelled = false
		t.mu.Unlock()
		s.paused, s.pausedID = nil, ""
		return t
	}
	var chatID *string
	if q.wake.Chat != nil {
		chatID = &q.wake.Chat.ChatID
	}
	return newTurn(s, q.wake.TurnID, chatID)
}

func (s *session) finishTurn(t *Turn, err error) {
	t.cancel()
	s.setCurrent(nil)
	if errors.Is(err, ErrBudgetExhausted) {
		s.mu.Lock()
		s.paused, s.pausedID = t, t.id
		s.mu.Unlock()
		_ = s.conn.notify(protocol.MethodError, errorParams{Code: CodeBudgetExhausted, Message: "Budget exhausted", TurnID: t.id})
		s.status(statusParams{State: "idle", PausedTurnID: t.id, Detail: "Waiting for budget"})
		return
	}
	_ = t.closeOutput()
	t.mu.Lock()
	needProfile := !t.profileSent && !t.resumed
	t.mu.Unlock()
	if needProfile {
		// Every turn needs a profile line (T3); fall back to the last one.
		line := s.currentOneLine()
		if ValidateOneLine(line) != nil {
			line = OneLine("Goal: unknown. Now: working.")
		}
		_ = t.Log(LogWarn, "turn ended without profile.update; repeated the last line", nil)
		_ = t.ProfileUpdate(line)
	}
	detail := ""
	switch {
	case t.wasCancelled() || s.turnCtx.Err() != nil:
		detail = "Cancelled"
	case err != nil:
		detail = "Failed"
		code := CodeInternalError
		var coded *CodedError
		if errors.As(err, &coded) {
			code = coded.Code
		}
		_ = s.conn.notify(protocol.MethodError, errorParams{Code: code, Message: OneLine(err.Error()), TurnID: t.id})
	}
	s.status(statusParams{State: "idle", TurnID: t.id, Detail: detail})
}

// heartbeat sends a heartbeat every interval (rule B1) until ctx ends.
func (s *session) heartbeat(ctx context.Context, interval time.Duration) {
	tick := s.clock.NewTicker(interval)
	defer tick.Stop()
	for {
		select {
		case <-tick.C():
			up := s.clock.Now().Sub(s.started).Milliseconds()
			_ = s.conn.notify(protocol.MethodHeartbeat, struct {
				UptimeMs int64 `json:"uptimeMs"`
			}{up})
		case <-ctx.Done():
			return
		}
	}
}
