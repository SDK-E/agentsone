// SPDX-License-Identifier: MIT

package domain

import (
	"errors"
	"fmt"
	"time"
)

// State is where one agent's runner session stands, as the daemon sees it.
type State string

// Session states.
const (
	// StateStopped: no runner process. Waking starts the unit.
	StateStopped State = "stopped"
	// StateStarting: the unit was started; the runner has not finished hello.
	StateStarting State = "starting"
	// StateWelcomed: the welcome was sent; waiting for the first status idle (H7, H8).
	StateWelcomed State = "welcomed"
	// StateIdle: ready for a wake.
	StateIdle State = "idle"
	// StateWorking: a turn is running.
	StateWorking State = "working"
	// StateSleeping: the runner compacted its session; the unit is being stopped.
	StateSleeping State = "sleeping"
	// StateFailed: the runner reported state error or went silent.
	StateFailed State = "failed"
)

// Errors of the session state machine.
var (
	// ErrOutOfOrder is a transition the protocol does not allow in this state.
	ErrOutOfOrder = errors.New("out of order")
	// ErrUnknownTurn names a turn that is not the running one (code 1001).
	ErrUnknownTurn = errors.New("unknown turn")
	// ErrUnknownRecipient is a bus.send to an agent that does not exist (code 1002).
	ErrUnknownRecipient = errors.New("unknown recipient")
	// ErrNotPermitted is an action the agent may not take (code 1003).
	ErrNotPermitted = errors.New("not permitted")
	// ErrUnknownTask is a task.done for a task that does not exist (code 1004).
	ErrUnknownTask = errors.New("unknown task")
	// ErrResumeUnavailable is the runner's answer when it cannot resume a paused turn (code 1011).
	ErrResumeUnavailable = errors.New("resume unavailable")
	// ErrRateLimited is too many bus messages (code 1030).
	ErrRateLimited = errors.New("rate limited")
)

// RunnerStatus is the content of a status notification.
type RunnerStatus struct {
	State        string // idle, working, sleeping or error
	TurnID       string
	PausedTurnID string
}

// StatusOutcome is what a status notification changed.
type StatusOutcome struct {
	// EndedTurn is the turn the status ended, if any.
	EndedTurn string
	// MissingProfile is true when EndedTurn ended without a profile.update
	// (PROTOCOL.md turns step 4, rule P1). The caller records a fault.
	MissingProfile bool
	// PausedTurn is the turn paused for budget, if any (rule R1).
	PausedTurn string
	// Failed is true when the runner reported state error.
	Failed bool
}

// Session is the daemon's view of one runner. It is not safe for concurrent
// use; the app layer guards it.
type Session struct {
	agent     string
	state     State
	lastHeard time.Time
	turn      string
	profiled  bool
	paused    string
}

// NewSession returns a stopped session for agent.
func NewSession(agent string) *Session { return &Session{agent: agent, state: StateStopped} }

// Agent is the agent's name.
func (s *Session) Agent() string { return s.agent }

// State is the current state.
func (s *Session) State() State { return s.state }

// Turn is the running turn id, or "".
func (s *Session) Turn() string { return s.turn }

// PausedTurn is the turn paused for budget, or "".
func (s *Session) PausedTurn() string { return s.paused }

// Start marks the unit as started. Allowed from stopped, sleeping and failed.
func (s *Session) Start(now time.Time) error {
	switch s.state {
	case StateStopped, StateSleeping, StateFailed:
		s.state, s.lastHeard, s.turn, s.profiled = StateStarting, now, "", false
		return nil
	default:
		return fmt.Errorf("start in state %s: %w", s.state, ErrOutOfOrder)
	}
}

// Welcome records that the welcome was sent. Allowed only while starting.
func (s *Session) Welcome(now time.Time) error {
	if s.state != StateStarting {
		return fmt.Errorf("welcome in state %s: %w", s.state, ErrOutOfOrder)
	}
	s.state, s.lastHeard = StateWelcomed, now
	return nil
}

// Heard records that any line arrived from the runner. Every line counts as
// a sign of life, not only heartbeats (PROTOCOL.md heartbeats).
func (s *Session) Heard(now time.Time) {
	if now.After(s.lastHeard) {
		s.lastHeard = now
	}
}

// Ready reports whether the daemon may send requests (wake, deliver, cancel,
// sleep). Rule H8: nothing but the hello response before the first status.
func (s *Session) Ready() bool { return s.state == StateIdle || s.state == StateWorking }

// Status applies a status notification.
func (s *Session) Status(st RunnerStatus) (StatusOutcome, error) {
	if s.state == StateWelcomed && st.State != "idle" {
		return StatusOutcome{}, fmt.Errorf("first status %s, want idle: %w", st.State, ErrOutOfOrder)
	}
	switch st.State {
	case "working":
		return s.working(st.TurnID)
	case "idle":
		return s.idle(st), nil
	case "sleeping":
		if s.state == StateWorking {
			return StatusOutcome{}, fmt.Errorf("sleeping during turn %s: %w", s.turn, ErrOutOfOrder)
		}
		s.state = StateSleeping
		return StatusOutcome{}, nil
	case "error":
		out := StatusOutcome{EndedTurn: s.turn, Failed: true}
		s.state, s.turn, s.profiled = StateFailed, "", false
		return out, nil
	default:
		return StatusOutcome{}, fmt.Errorf("status state %q: %w", st.State, ErrOutOfOrder)
	}
}

func (s *Session) working(turn string) (StatusOutcome, error) {
	if s.state != StateIdle || turn == "" {
		return StatusOutcome{}, fmt.Errorf("working in state %s: %w", s.state, ErrOutOfOrder)
	}
	s.state, s.turn = StateWorking, turn
	// A resumed paused turn needs no new profile.update (rule R2).
	s.profiled = turn == s.paused
	if turn == s.paused {
		s.paused = ""
	}
	return StatusOutcome{}, nil
}

func (s *Session) idle(st RunnerStatus) StatusOutcome {
	var out StatusOutcome
	if s.state == StateWorking {
		out.EndedTurn = s.turn
		out.MissingProfile = !s.profiled
	}
	if st.PausedTurnID != "" {
		s.paused, out.PausedTurn = st.PausedTurnID, st.PausedTurnID
		out.MissingProfile = false
		if out.EndedTurn == "" {
			out.EndedTurn = st.PausedTurnID
		}
	}
	s.state, s.turn, s.profiled = StateIdle, "", false
	return out
}

// Profile records a profile.update for turn. It must name the running turn
// (rule T6); otherwise ErrUnknownTurn.
func (s *Session) Profile(turn string) error {
	if s.state != StateWorking || turn != s.turn {
		return fmt.Errorf("profile.update for %q: %w", turn, ErrUnknownTurn)
	}
	s.profiled = true
	return nil
}

// InTurn reports whether turn is the running turn (rule T6 for output,
// usage, bus.send and task.done).
func (s *Session) InTurn(turn string) bool { return s.state == StateWorking && turn == s.turn }

// Stop marks the runner process as gone. A paused turn survives a stop; the
// next welcome carries it (PROTOCOL.md budget and resume step 4).
func (s *Session) Stop() { s.state, s.turn, s.profiled = StateStopped, "", false }

// Fail marks the session failed, for example after a heartbeat timeout.
func (s *Session) Fail() { s.state, s.turn, s.profiled = StateFailed, "", false }

// Expired reports whether the runner has been silent for timeout or longer
// (rule B2). Only a connected runner can expire; the hello deadline before
// the welcome belongs to the connection.
func (s *Session) Expired(now time.Time, timeout time.Duration) bool {
	switch s.state {
	case StateWelcomed, StateIdle, StateWorking:
		return !now.Before(s.lastHeard.Add(timeout))
	default:
		return false
	}
}
