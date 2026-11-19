// SPDX-License-Identifier: MIT

package domain

// WakeAction is what the supervisor does with a wake.
type WakeAction int

// Wake actions.
const (
	// WakeRefuse: the agent is frozen; nothing starts (host freeze lever).
	WakeRefuse WakeAction = iota + 1
	// WakeStart: start the unit, hold the wake until the runner is ready.
	WakeStart
	// WakeHold: the runner is starting; hold the wake until it is ready.
	WakeHold
	// WakeSend: send the wake now. A runner in a turn queues it (rule T7).
	WakeSend
)

// PlanWake decides how to handle a wake for a session in state s. Sleeping
// costs nothing: a sleeping or stopped agent has no process, so a wake starts
// one and the welcome hands it the stored summary.
func PlanWake(frozen bool, s State) WakeAction {
	if frozen {
		return WakeRefuse
	}
	switch s {
	case StateIdle, StateWorking:
		return WakeSend
	case StateStarting, StateWelcomed:
		return WakeHold
	default:
		return WakeStart
	}
}
