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

// numberWakeLocked numbers the next wake handed to a runner. With the lock
// held.
func (s *Supervisor) numberWakeLocked() uint64 {
	s.wakes++
	return s.wakes
}

// wakeSent takes the outcome of sending req, wake number seq, to agent's
// runner. A runner's refusal that differs from the last one is logged at
// error level, audited and shown on the agent; the same refusal again
// changes nothing. An accepted wake clears what was shown.
//
// Wakes to one runner can be in flight at once (the held wakes the first
// status releases, and wakes sent straight to it), so their outcomes can
// come here in any order. What the agent shows follows the newest wake: an
// outcome older than the newest one taken neither clears nor shows a
// refusal. An older refusal that differs from the one shown is still logged
// and audited, since the runner did refuse it. Other errors (the connection
// ended) are the caller's to handle.
//
// Two answers can also arrive at once. Each decides what the agent shows
// and tells the society within one show turn, so the society hears them in
// the order they were decided and the last word it hears is the newest.
func (s *Supervisor) wakeSent(ctx context.Context, agent string, seq uint64, req wakeRequest, err error) {
	ref, refused := refusedWake(req, err)
	if err != nil && !refused {
		return
	}
	defer s.showTurn(agent)()
	s.mu.Lock()
	newest := seq > s.answered[agent]
	if newest {
		s.answered[agent] = seq
	}
	prev, shown := s.refused[agent]
	same := refused && shown && prev == ref
	if newest && !same {
		if refused {
			s.refused[agent] = ref
		} else {
			delete(s.refused, agent)
		}
	}
	s.mu.Unlock()
	switch {
	case !refused:
		if newest && shown {
			s.runnerRunning(ctx, agent)
		}
	case !same:
		s.log().Error(LogWakeRefused, "agent", agent, "reason", string(ref.reason), "code", ref.code, "message", ref.message)
		s.audit(ctx, ActionWakeRefused, "agent:"+agent, map[string]any{"reason": string(ref.reason), "code": ref.code, "message": ref.message})
		if newest {
			s.runnerFailed(ctx, agent, ref.stateReason())
		}
	}
}

// forgetRefusal drops what was remembered of agent's refusals and tells the
// society the runner runs: it started anew, so the society's failure is gone
// and a refusal is news again. It takes a show turn, like wakeSent.
func (s *Supervisor) forgetRefusal(ctx context.Context, agent string) {
	defer s.showTurn(agent)()
	s.mu.Lock()
	delete(s.refused, agent)
	s.mu.Unlock()
	s.runnerRunning(ctx, agent)
}

// showTurn waits for agent's show turn and returns its end. Within a turn,
// what a refusal or its end makes the agent show is decided under s.mu and
// told to the society after s.mu is released (the society takes its own
// locks), so no other decision for that agent comes in between. Take it
// without s.mu held. The turn is a channel rather than a mutex so a test
// bubble (testing/synctest) sees a wait for it as blocked.
func (s *Supervisor) showTurn(agent string) (end func()) {
	s.mu.Lock()
	turn, ok := s.telling[agent]
	if !ok {
		turn = make(chan struct{}, 1)
		s.telling[agent] = turn
	}
	s.mu.Unlock()
	turn <- struct{}{}
	return func() { <-turn }
}
