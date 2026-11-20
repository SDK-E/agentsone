// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/SDK-E/agentsone/internal/core"
)

// ActionWakeRefused is the audit action for a runner that refused a wake,
// on the target agent:<name>. One record is written per change of what the
// runner refuses, not per attempt.
const ActionWakeRefused = "agent.wake.refused"

// LogWakeRefused is the error line for a runner's refusal of a wake.
const LogWakeRefused = "runner refused a wake"

// maxRefusalMessage bounds the runner's message in the line the society shows.
const maxRefusalMessage = 120

// runnerError is an error the runner answered a request with, as the
// connection returns it (adapters/rpc.RemoteError).
type runnerError interface {
	error
	RPCCode() int
	RPCMessage() string
}

// refusal is what a runner refused: the kind of wake, and its answer.
type refusal struct {
	reason  core.WakeReason
	code    int
	message string
}

// refusedWake returns the runner's refusal in err, if err holds one.
func refusedWake(req wakeRequest, err error) (refusal, bool) {
	var re runnerError
	if !errors.As(err, &re) {
		return refusal{}, false
	}
	return refusal{reason: req.reason, code: re.RPCCode(), message: re.RPCMessage()}, true
}

// stateReason is the one plain line the society shows under the agent.
func (r refusal) stateReason() string {
	msg := []rune(r.message)
	if len(msg) > maxRefusalMessage {
		msg = append(msg[:maxRefusalMessage-1], '…')
	}
	return fmt.Sprintf("The runner refused a %s wake: error %d, %s.", r.reason, r.code, string(msg))
}

// wakeSent takes the outcome of sending req to agent's runner. A runner's
// refusal that differs from the last one is logged at error level, audited
// and shown on the agent; the same refusal again changes nothing. An
// accepted wake clears what was shown. Other errors (the connection ended)
// are the caller's to handle.
func (s *Supervisor) wakeSent(ctx context.Context, agent string, req wakeRequest, err error) {
	if err == nil {
		s.mu.Lock()
		_, shown := s.refused[agent]
		delete(s.refused, agent)
		s.mu.Unlock()
		if shown {
			s.runnerRunning(ctx, agent)
		}
		return
	}
	ref, ok := refusedWake(req, err)
	if !ok {
		return
	}
	s.mu.Lock()
	prev, shown := s.refused[agent]
	s.refused[agent] = ref
	s.mu.Unlock()
	if shown && prev == ref {
		return
	}
	s.log().Error(LogWakeRefused, "agent", agent, "reason", string(ref.reason), "code", ref.code, "message", ref.message)
	s.audit(ctx, ActionWakeRefused, "agent:"+agent, map[string]any{"reason": string(ref.reason), "code": ref.code, "message": ref.message})
	s.runnerFailed(ctx, agent, ref.stateReason())
}

// forgetRefusal drops what was remembered of agent's refusals: its runner
// started anew, so the society's failure is gone and a refusal is news again.
func (s *Supervisor) forgetRefusal(agent string) {
	s.mu.Lock()
	delete(s.refused, agent)
	s.mu.Unlock()
}
