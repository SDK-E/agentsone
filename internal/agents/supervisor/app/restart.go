// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/SDK-E/agentsone/internal/agents/supervisor/domain"
)

// Audit actions of a runner that keeps failing (SDK-2082), on the agent:
// one when a spell of failures begins and one when the runner recovers,
// never one per restart.
const (
	ActionRunnerFailing   = "agent.runner.failing"
	ActionRunnerRecovered = "agent.runner.recovered"
)

// ReasonRunnerFailing is shown under an agent whose runner exited with a
// failure and waits to start again; the last error follows it.
const ReasonRunnerFailing = "The runner keeps failing and starts again by itself."

// Log lines of the restart backoff: one per failure, so one per backoff
// step, and one when the runner recovered.
const (
	LogRunnerFailing   = "runner failed; it starts again after a wait"
	LogRunnerRecovered = "runner recovered: it stayed up after failing"
)

// RunnerExited is the launcher's word that agent name's runner process
// ended without being stopped; err is why, nil for a clean exit. The
// launcher never starts a runner by itself: the supervisor decides. A runner
// that failed starts again after its backoff (Tick), through the same
// network wall check as every other start; one that exited cleanly stays
// stopped until something wakes it.
func (s *Supervisor) RunnerExited(ctx context.Context, name string, err error) {
	s.mu.Lock()
	r, ok := s.runners[name]
	if !ok {
		s.mu.Unlock()
		return // retired meanwhile
	}
	s.dropConnLocked(r)
	if err == nil {
		r.session.Stop()
		s.mu.Unlock()
		return
	}
	r.session.Fail()
	s.mu.Unlock()
	last := oneLine(err.Error())
	s.restartLater(ctx, name, r, last, err)
}

// restartLater sets when the failed runner of agent name starts again. The
// agent shows the runner failing with last, a plain line for a human; cause
// goes to the log.
func (s *Supervisor) restartLater(ctx context.Context, name string, r *runner, last string, cause error) {
	s.mu.Lock()
	wait, began := r.backoff.Fail()
	r.restartAt = s.d.Clock.Now().Add(wait)
	s.mu.Unlock()
	if began {
		s.audit(ctx, ActionRunnerFailing, "agent:"+name, map[string]any{"error": last})
	}
	s.log().Warn(LogRunnerFailing, "agent", name, "err", cause.Error(), "wait", wait.String())
	s.runnerFailed(ctx, name, ReasonRunnerFailing+" Last error: "+strings.TrimSuffix(last, ".")+".")
}

// restartDue starts again the failed runners whose wait is over. The
// network wall is checked afresh first, not taken from the last check: the
// machine may have changed since, and no restart goes out through a wall
// that does not hold. Runners that wait for the wall start once it holds.
func (s *Supervisor) restartDue(ctx context.Context, now time.Time) {
	due := s.dueRestarts(now)
	if len(due) == 0 {
		return
	}
	if w, _ := s.CheckWall(ctx); !w.Holds {
		for _, name := range due {
			s.markWallBroken(ctx, name, w, false)
		}
		return
	}
	for _, name := range due {
		s.restart(ctx, name)
	}
}

// dueRestarts names the runners whose restart is due at now. A runner
// started meanwhile (a reconfigure) has no restart left to make.
func (s *Supervisor) dueRestarts(now time.Time) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var due []string
	for name, r := range s.runners {
		switch {
		case r.restartAt.IsZero():
		case r.session.State() != domain.StateFailed:
			r.restartAt = time.Time{}
		case !now.Before(r.restartAt):
			due = append(due, name)
		}
	}
	slices.Sort(due)
	return due
}

// restart starts a failed runner again through the launcher, so through
// the wall check. A frozen agent is not restarted; it starts on its next
// wake after the release.
func (s *Supervisor) restart(ctx context.Context, name string) {
	frozen, err := s.Frozen(ctx, name)
	if err != nil {
		s.log().Warn("supervisor: cannot tell whether a failed runner may start again", "agent", name, "err", err)
		return // the restart stays due for the next Tick
	}
	s.mu.Lock()
	r, ok := s.runners[name]
	if !ok || r.restartAt.IsZero() || r.session.State() != domain.StateFailed {
		s.mu.Unlock()
		return
	}
	r.restartAt = time.Time{}
	if frozen {
		s.mu.Unlock()
		return
	}
	_ = r.session.Start(s.d.Clock.Now())
	s.mu.Unlock()
	err = s.d.Launcher.Start(ctx, name)
	if err == nil {
		return
	}
	s.mu.Lock()
	r.session.Fail()
	if errors.Is(err, ErrNetworkWall) {
		// The wall broke between the check and the start: start once it holds.
		r.restartAt = s.d.Clock.Now()
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()
	s.restartLater(ctx, name, r, ReasonStartFailed, err)
}

// recoverStable ends the spell of failures of every runner that stayed up
// for domain.RestartStableAfter: one audit record and one log line each.
func (s *Supervisor) recoverStable(ctx context.Context, now time.Time) {
	var recovered []string
	s.mu.Lock()
	for name, r := range s.runners {
		if r.session.Ready() && r.backoff.Recovered(now) {
			recovered = append(recovered, name)
		}
	}
	s.mu.Unlock()
	slices.Sort(recovered)
	for _, name := range recovered {
		s.audit(ctx, ActionRunnerRecovered, "agent:"+name, nil)
		s.log().Info(LogRunnerRecovered, "agent", name)
	}
}

// oneLine is text on one line, its runs of white space made one space.
func oneLine(text string) string { return strings.Join(strings.Fields(text), " ") }
