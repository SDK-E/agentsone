// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"errors"
	"log/slog"

	"github.com/SDK-E/agentsone/internal/agents/supervisor/domain"
	"github.com/SDK-E/agentsone/internal/core"
)

// Reasons the society shows under a failed agent. They are plain sentences
// for a human: an error's detail (paths, key names, stack) stays in the
// audit trail.
const (
	ReasonGatewayUnreachable = "The model gateway is not reachable."
	ReasonGatewayToken       = "The model gateway token could not be issued."
	ReasonChartLoop          = "The chart loops above this agent, so it cannot reach the model gateway."
	ReasonStartFailed        = "The runner could not be started."
	ReasonRunnerError        = "The runner stopped with an error."
)

// gatewayReason says in plain words why the agent has no gateway token.
func gatewayReason(err error) string {
	switch {
	case errors.Is(err, core.ErrUnavailable):
		return ReasonGatewayUnreachable
	case errors.Is(err, core.ErrInvalid):
		return ReasonChartLoop
	default:
		return ReasonGatewayToken
	}
}

// runnerFailed tells the society why agent's runner cannot run.
func (s *Supervisor) runnerFailed(ctx context.Context, agent, reason string) {
	if s.d.States == nil {
		return
	}
	if err := s.d.States.RunnerFailed(ctx, agent, reason); err != nil {
		slog.Default().Warn("supervisor: society did not take the runner failure", "agent", agent, "err", err)
	}
}

// runnerState tells the society what agent's runner is doing now. Call it
// without the supervisor's lock held: the society takes its own.
func (s *Supervisor) runnerState(ctx context.Context, agent string, state RunState) {
	if s.d.Runs == nil {
		return
	}
	if err := s.d.Runs.RunnerState(ctx, agent, state); err != nil {
		slog.Default().Warn("supervisor: society did not take the runner state", "agent", agent, "state", string(state), "err", err)
	}
}

// turnEnded tells the society a turn of agent just ended.
func (s *Supervisor) turnEnded(ctx context.Context, agent string) {
	if s.d.Runs == nil {
		return
	}
	if err := s.d.Runs.TurnEnded(ctx, agent, s.d.Clock.Now()); err != nil {
		slog.Default().Warn("supervisor: society did not take the end of the turn", "agent", agent, "err", err)
	}
}

// runStateOf maps a session state to the run state the society shows. A
// failed session has none: the failure is told with its reason.
func runStateOf(st domain.State) (RunState, bool) {
	switch st {
	case domain.StateStopped, domain.StateSleeping:
		return RunStopped, true
	case domain.StateStarting, domain.StateWelcomed:
		return RunStarting, true
	case domain.StateIdle:
		return RunIdle, true
	case domain.StateWorking:
		return RunWorking, true
	default:
		return "", false
	}
}

// runnerRunning tells the society agent's runner is ready again.
func (s *Supervisor) runnerRunning(ctx context.Context, agent string) {
	if s.d.States == nil {
		return
	}
	if err := s.d.States.RunnerRunning(ctx, agent); err != nil {
		slog.Default().Warn("supervisor: society did not take the runner recovery", "agent", agent, "err", err)
	}
}
