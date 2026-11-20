// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/SDK-E/agentsone/internal/agents/supervisor/domain"
	"github.com/SDK-E/agentsone/internal/core"
)

// ErrNetworkWall is a wake refused because the network wall does not hold.
// It is wrapped with core.ErrUnavailable.
var ErrNetworkWall = errors.New("the network wall does not hold")

// Audit actions for the network wall, on the target network:wall.
const (
	ActionWallBroken   = "net.wall.broken"
	ActionWallRestored = "net.wall.restored"
	// ActionRunnerWallStop is on the agent: its runner was stopped because
	// the wall broke while it was up.
	ActionRunnerWallStop = "agent.runner.wall_stop"
)

// Log lines for the network wall. Operators who cannot open a shell in the
// machine prove the wall with them, so their text stays stable: the holds
// line goes on with ", interfaces: eth0".
const (
	LogWallHolds  = "network wall holds: no default route"
	LogWallBroken = "network wall broken: no agent will wake"
)

const (
	wallTarget         = "network:wall"
	defaultWallRecheck = 5 * time.Minute
)

// wallUnchecked stands until the daemon checked the wall.
var wallUnchecked = domain.NetWall{Reason: "the network wall has not been checked yet", Fix: "Restart the machine"}

// CheckWall checks the network wall and keeps the answer: while it does not
// hold, Wake refuses every agent. The daemon calls it at start, before any
// agent wakes, and Tick every WallRecheck. A changed answer is logged once
// and, when the wall breaks or comes back, recorded in the audit trail;
// every active agent then shows why it cannot wake, or no longer does.
func (s *Supervisor) CheckWall(ctx context.Context) (domain.NetWall, bool) {
	w := domain.NetWallUnproven(errors.New("no network wall check is wired"))
	if s.d.Wall != nil {
		w = s.d.Wall.Check(ctx)
	}
	s.mu.Lock()
	prev, checked := s.wall, s.wallChecked
	s.wall, s.wallAt, s.wallChecked = w, s.d.Clock.Now(), true
	s.mu.Unlock()
	if checked && prev.Same(w) {
		return w, false
	}
	s.logWall(w)
	switch {
	case !w.Holds:
		s.audit(ctx, ActionWallBroken, wallTarget, map[string]any{"reason": w.Reason, "interfaces": w.Interfaces, "routesOut": w.RoutesOut})
		s.stopForWall(ctx)
		s.markAllWallBroken(ctx, w)
	case checked && !prev.Holds:
		s.audit(ctx, ActionWallRestored, wallTarget, map[string]any{"interfaces": w.Interfaces})
		s.clearWallMarks(ctx)
		s.runWantedAgain(ctx)
	}
	return w, true
}

// wallLauncher refuses to start or restart a runner while the network wall
// does not hold. It wraps the launcher so that no path to a running runner
// can skip the check.
type wallLauncher struct {
	Launcher
	s *Supervisor
}

func (l wallLauncher) Start(ctx context.Context, agent string) error {
	if w, ok := l.s.wallRefusal(); !ok {
		return wallRefused(agent, w)
	}
	return l.Launcher.Start(ctx, agent)
}

func (l wallLauncher) Restart(ctx context.Context, agent string) error {
	if w, ok := l.s.wallRefusal(); !ok {
		return wallRefused(agent, w)
	}
	return l.Launcher.Restart(ctx, agent)
}

func wallRefused(agent string, w domain.NetWall) error {
	return fmt.Errorf("%s: %w: %s: %w", agent, ErrNetworkWall, w.Reason, core.ErrUnavailable)
}

// stopForWall stops every runner that is up when the wall breaks. Each stop
// is recorded in the audit trail; the always-on agents among them start
// again when the wall holds.
func (s *Supervisor) stopForWall(ctx context.Context) {
	kinds := map[string]core.AgentKind{}
	all, listErr := s.d.Ports.Agents.ListAgents(ctx)
	if listErr == nil {
		for _, a := range all {
			kinds[a.Name] = a.Kind
		}
	}
	var stopped []string
	s.mu.Lock()
	for name, r := range s.runners {
		switch r.session.State() {
		case domain.StateStopped, domain.StateSleeping, domain.StateFailed:
			continue
		}
		s.dropConnLocked(r)
		r.session.Stop()
		r.pending = nil
		stopped = append(stopped, name)
		if kinds[name] == core.AgentAlwaysOn {
			s.runWanted[name] = true
		}
	}
	s.mu.Unlock()
	slices.Sort(stopped)
	if listErr != nil && len(stopped) > 0 {
		// Without the agents' kinds no runner is known to be always-on, so
		// none starts again by itself when the wall holds.
		s.log().Error("supervisor: the runners stopped by the broken network wall will not start again by themselves",
			"agents", strings.Join(stopped, ", "), "err", listErr)
	}
	for _, name := range stopped {
		if err := s.d.Launcher.Stop(ctx, name); err != nil {
			s.log().Warn("supervisor: a runner did not stop when the network wall broke", "agent", name, "err", err)
		}
		s.audit(ctx, ActionRunnerWallStop, "agent:"+name, map[string]any{"reason": "the network wall does not hold"})
	}
}

// runWantedAgain starts the runners whose idle start the broken wall
// refused. A runner that still cannot start shows why on its agent.
func (s *Supervisor) runWantedAgain(ctx context.Context) {
	s.mu.Lock()
	wanted := slices.Sorted(maps.Keys(s.runWanted))
	clear(s.runWanted)
	s.mu.Unlock()
	for _, name := range wanted {
		if err := s.Run(ctx, name); err != nil {
			s.log().Warn("supervisor: a runner did not start once the network wall held", "agent", name, "err", err)
		}
	}
}

// wallRefusal is the wall when it does not hold; ok is true when it holds.
func (s *Supervisor) wallRefusal() (domain.NetWall, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.wall, s.wall.Holds
}

// wallDue reports whether Tick should check the wall again at now.
func (s *Supervisor) wallDue(now time.Time) bool {
	every := s.d.Config.WallRecheck
	if every <= 0 {
		every = defaultWallRecheck
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.wallChecked && now.Sub(s.wallAt) >= every
}

// markWallBroken shows the wall's reason on agent, and remembers to clear
// it once the wall holds again. An agent that shows it already is told
// again only when the reason changed (again is true).
func (s *Supervisor) markWallBroken(ctx context.Context, agent string, w domain.NetWall, again bool) {
	s.mu.Lock()
	known := s.wallMarked[agent]
	s.wallMarked[agent] = true
	s.mu.Unlock()
	if !known || again {
		s.runnerFailed(ctx, agent, w.StateReason())
	}
}

func (s *Supervisor) markAllWallBroken(ctx context.Context, w domain.NetWall) {
	all, err := s.d.Ports.Agents.ListAgents(ctx)
	if err != nil {
		s.log().Warn("supervisor: cannot list the agents to show why they cannot wake", "err", err)
		return
	}
	for _, a := range all {
		s.markWallBroken(ctx, a.Name, w, true)
	}
}

func (s *Supervisor) clearWallMarks(ctx context.Context) {
	s.mu.Lock()
	marked := slices.Sorted(maps.Keys(s.wallMarked))
	clear(s.wallMarked)
	s.mu.Unlock()
	if s.d.States == nil {
		return
	}
	for _, agent := range marked {
		if err := s.d.States.RunnerCleared(ctx, agent); err != nil {
			s.log().Warn("supervisor: society did not take that the agent may wake again", "agent", agent, "err", err)
		}
	}
}

// logWall writes the one line for the wall's answer.
func (s *Supervisor) logWall(w domain.NetWall) {
	ifaces := strings.Join(w.Interfaces, ", ")
	if ifaces == "" {
		ifaces = "none"
	}
	if w.Holds {
		s.log().Info(LogWallHolds+", interfaces: "+ifaces, "interfaces", ifaces)
		return
	}
	attrs := []any{"reason", w.Reason, "fix", w.Fix, "interfaces", ifaces, "routes_out", strings.Join(w.RoutesOut, "; ")}
	if w.Err != nil {
		attrs = append(attrs, "err", w.Err)
	}
	s.log().Error(LogWallBroken, attrs...)
}

func (s *Supervisor) log() *slog.Logger {
	if s.d.Log != nil {
		return s.d.Log
	}
	return slog.Default()
}
