// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"errors"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/SDK-E/agentsone/internal/agents/supervisor/domain"
)

// ErrNetworkWall is a wake refused because the network wall does not hold.
// It is wrapped with core.ErrUnavailable.
var ErrNetworkWall = errors.New("the network wall does not hold")

// Audit actions for the network wall, on the target network:wall.
const (
	ActionWallBroken   = "net.wall.broken"
	ActionWallRestored = "net.wall.restored"
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
		s.markAllWallBroken(ctx, w)
	case checked && !prev.Holds:
		s.audit(ctx, ActionWallRestored, wallTarget, map[string]any{"interfaces": w.Interfaces})
		s.clearWallMarks(ctx)
	}
	return w, true
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
// it once the wall holds again.
func (s *Supervisor) markWallBroken(ctx context.Context, agent string, w domain.NetWall) {
	s.mu.Lock()
	s.wallMarked[agent] = true
	s.mu.Unlock()
	s.runnerFailed(ctx, agent, w.StateReason())
}

func (s *Supervisor) markAllWallBroken(ctx context.Context, w domain.NetWall) {
	all, err := s.d.Ports.Agents.ListAgents(ctx)
	if err != nil {
		s.log().Warn("supervisor: cannot list the agents to show why they cannot wake", "err", err)
		return
	}
	for _, a := range all {
		s.markWallBroken(ctx, a.Name, w)
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
