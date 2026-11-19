// SPDX-License-Identifier: MIT

package domain

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Terminal limits. The caps keep one actor, or all of them, from filling
// the machine with shells; idle terminals with no client are closed.
const (
	MaxPtyPerActor = 4
	MaxPtyTotal    = 32
	PtyIdleTimeout = 30 * time.Minute

	defaultCols, minCols, maxCols = 120, 10, 1000
	defaultRows, minRows, maxRows = 32, 5, 500

	// fallbackShell serves agents whose account has no login shell.
	fallbackShell = "/bin/bash"
	noLogin       = "/usr/sbin/nologin"
)

// ErrTooMany is a request over a terminal cap.
var ErrTooMany = errors.New("too many terminals")

// PtySpec is what a new terminal runs and its size.
type PtySpec struct {
	Command []string
	Cols    int
	Rows    int
}

// NewPtySpec checks a terminal request. With no command it starts the
// caller's login shell.
func NewPtySpec(command []string, cols, rows *int, shell string) (PtySpec, error) {
	s := PtySpec{Command: command, Cols: defaultCols, Rows: defaultRows}
	if len(s.Command) == 0 {
		if shell == "" || shell == noLogin {
			shell = fallbackShell
		}
		s.Command = []string{shell, "-l"}
	}
	if s.Command[0] == "" {
		return PtySpec{}, invalid("terminal command needs a program")
	}
	if cols != nil {
		s.Cols = *cols
	}
	if rows != nil {
		s.Rows = *rows
	}
	if err := checkSize(s.Cols, s.Rows); err != nil {
		return PtySpec{}, err
	}
	return s, nil
}

func checkSize(cols, rows int) error {
	if cols < minCols || cols > maxCols || rows < minRows || rows > maxRows {
		return invalid("terminal size %dx%d is outside %d-%d columns and %d-%d rows", cols, rows, minCols, maxCols, minRows, maxRows)
	}
	return nil
}

// PtySession is one terminal.
type PtySession struct {
	ID         string
	Owner      string // actor label
	UID        int
	User       string
	Spec       PtySpec
	Created    time.Time
	LastActive time.Time
	Connected  bool
	Exited     bool
	ExitCode   *int
}

// VisibleTo reports whether a may see and use the terminal: its owner, root
// agents and humans.
func (s PtySession) VisibleTo(a Actor) bool { return a.Privileged() || s.Owner == a.Label() }

// Idle reports whether the terminal had no client for longer than the idle
// timeout.
func (s PtySession) Idle(now time.Time) bool {
	return !s.Connected && now.Sub(s.LastActive) > PtyIdleTimeout
}

// CanOpenPty applies the terminal caps to the running terminals in open.
func CanOpenPty(a Actor, open []PtySession) error {
	total, mine := 0, 0
	for _, s := range open {
		if s.Exited {
			continue
		}
		total++
		if s.Owner == a.Label() {
			mine++
		}
	}
	if mine >= MaxPtyPerActor {
		return fmt.Errorf("%s already has %d terminals: %w", a.Label(), mine, ErrTooMany)
	}
	if total >= MaxPtyTotal {
		return fmt.Errorf("the machine already runs %d terminals: %w", total, ErrTooMany)
	}
	return nil
}

// PtyResize is the client's resize control message.
type PtyResize struct {
	Cols int
	Rows int
}

// ParsePtyControl reads a text frame from the client.
func ParsePtyControl(b []byte) (PtyResize, error) {
	var m struct {
		Type string `json:"type"`
		Cols int    `json:"cols"`
		Rows int    `json:"rows"`
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return PtyResize{}, invalid("terminal control message is not JSON")
	}
	if m.Type != "resize" {
		return PtyResize{}, invalid("terminal control type %q", m.Type)
	}
	if err := checkSize(m.Cols, m.Rows); err != nil {
		return PtyResize{}, err
	}
	return PtyResize{Cols: m.Cols, Rows: m.Rows}, nil
}

// PtyExitMessage is the server's text frame when the program ends.
func PtyExitMessage(code int) []byte {
	return fmt.Appendf(nil, `{"type":"exit","code":%d}`, code)
}
