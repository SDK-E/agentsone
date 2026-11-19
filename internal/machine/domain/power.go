// SPDX-License-Identifier: MIT

package domain

import "time"

// RebootExitCode is the daemon's exit status when an agent or human asks for
// a reboot. tini passes it on, and the compose restart policy brings the
// container back with a fresh root file system.
const RebootExitCode = 75

const (
	defaultRebootDelay = 10 * time.Second
	maxRebootDelay     = time.Hour
	maxReasonBytes     = 500
)

// Reboot is a scheduled restart of the machine.
type Reboot struct {
	Reason      string
	RequestedBy string
	At          time.Time
	Delay       time.Duration
}

// NewReboot checks a reboot request. Shutdown and start are human powers held
// by Docker or Coolify and are not offered here.
func NewReboot(a Actor, reason string, delaySeconds *int, now time.Time) (Reboot, error) {
	if reason == "" || len(reason) > maxReasonBytes {
		return Reboot{}, invalid("reboot reason must be 1 to %d bytes", maxReasonBytes)
	}
	delay := defaultRebootDelay
	if delaySeconds != nil {
		delay = time.Duration(*delaySeconds) * time.Second
	}
	if delay < 0 || delay > maxRebootDelay {
		return Reboot{}, invalid("reboot delay must be 0 to 3600 seconds")
	}
	return Reboot{Reason: reason, RequestedBy: a.Label(), At: now.Add(delay), Delay: delay}, nil
}
