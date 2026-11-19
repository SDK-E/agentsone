// SPDX-License-Identifier: MIT

package domain

import (
	"slices"
	"time"
)

// Signal is a signal the API may send.
type Signal string

// Signals the contract allows.
const (
	SignalTerm Signal = "TERM"
	SignalKill Signal = "KILL"
	SignalHup  Signal = "HUP"
	SignalInt  Signal = "INT"
	SignalStop Signal = "STOP"
	SignalCont Signal = "CONT"
	SignalUsr1 Signal = "USR1"
	SignalUsr2 Signal = "USR2"
)

// Signals lists every allowed signal.
func Signals() []Signal {
	return []Signal{SignalTerm, SignalKill, SignalHup, SignalInt, SignalStop, SignalCont, SignalUsr1, SignalUsr2}
}

// ParseSignal checks a signal name.
func ParseSignal(s string) (Signal, error) {
	if !slices.Contains(Signals(), Signal(s)) {
		return "", invalid("signal %q", s)
	}
	return Signal(s), nil
}

// Process is one running process.
type Process struct {
	PID        int
	PPID       int
	UID        int
	User       string
	Command    string
	CPUPercent float64
	RSSBytes   int64
	Started    time.Time
	Unit       string
	Agent      string
}

// CanSignal lets custom agents signal only their own processes. Nobody may
// signal PID 1 (tini): ending it ends the container, which is reboot's job.
func CanSignal(a Actor, p Process) error {
	if p.PID == 1 {
		return forbidden("pid 1 holds the machine; use reboot")
	}
	if !a.Privileged() && p.UID != a.UID {
		return forbidden("%s may signal only its own processes", a.Label())
	}
	return nil
}
