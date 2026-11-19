// SPDX-License-Identifier: MIT

package system

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/machine/app"
	"github.com/SDK-E/agentsone/internal/machine/domain"
)

// signals maps contract signal names to kernel signals; a fixed lookup.
var signals = map[domain.Signal]syscall.Signal{
	domain.SignalTerm: syscall.SIGTERM, domain.SignalKill: syscall.SIGKILL,
	domain.SignalHup: syscall.SIGHUP, domain.SignalInt: syscall.SIGINT,
	domain.SignalStop: syscall.SIGSTOP, domain.SignalCont: syscall.SIGCONT,
	domain.SignalUsr1: syscall.SIGUSR1, domain.SignalUsr2: syscall.SIGUSR2,
}

// Kill sends s to pid with the kernel.
func Kill(pid int, s domain.Signal) error {
	sig, ok := signals[s]
	if !ok {
		return fmt.Errorf("signal %s: %w", s, core.ErrInvalid)
	}
	err := syscall.Kill(pid, sig)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, syscall.ESRCH):
		return fmt.Errorf("pid %d: %w", pid, core.ErrNotFound)
	case errors.Is(err, syscall.EPERM):
		return fmt.Errorf("pid %d: %w", pid, core.ErrForbidden)
	}
	return fmt.Errorf("signal pid %d: %w", pid, err)
}

// Power ends the daemon process and runs timers on the wall clock.
type Power struct{}

var (
	_ app.Power = Power{}
	_ app.Timer = Power{}
)

// Exit ends the daemon with code. tini exits with it and the compose
// restart policy starts the container again.
func (Power) Exit(code int) { os.Exit(code) }

// AfterFunc runs f once after d.
func (Power) AfterFunc(d time.Duration, f func()) { time.AfterFunc(d, f) }
