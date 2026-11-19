// SPDX-License-Identifier: MIT

// Package app holds the bus use cases: channels, sending messages to agents
// and channels, waking direct-message recipients, listing history and
// streaming. It depends on the domain ports and on core.Ports, never on a
// concrete adapter.
package app

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/SDK-E/agentsone/internal/bus/domain"
	"github.com/SDK-E/agentsone/internal/core"
)

// Deps are what the service needs.
type Deps struct {
	Channels domain.ChannelRepository
	Messages domain.MessageRepository
	Clock    core.Clock
	IDs      core.IDs
	// Ports is read at use time: other modules fill it in their Provide.
	Ports *core.Ports
	Log   *slog.Logger
}

// sendRate bounds each sender. It stops message loops between agents, which
// would otherwise wake each other for ever.
var sendRate = domain.Rate{Max: 120, Window: time.Minute}

const (
	// defaultLimit and maxLimit bound one page of a list.
	defaultLimit = 50
	maxLimit     = 200
	// batch is how many stored rows one read fetches while the service skips
	// rows the caller may not see.
	batch = 200
)

var systemCaller = core.Caller{Kind: core.CallerSystem, Name: "daemon", IsRoot: true}

// Service is the bus bounded context.
type Service struct {
	d Deps
	// mu makes append plus broadcast one step, so a subscriber that
	// registers under mu sees every message exactly once.
	mu      sync.Mutex
	hub     *hub
	limiter *domain.Limiter
}

var _ core.BusSender = (*Service)(nil)

// New returns the service.
func New(d Deps) *Service {
	return &Service{d: d, hub: newHub(), limiter: domain.NewLimiter(d.Clock)}
}

// callerOf returns the caller in ctx. Work with no caller is the daemon's own.
func callerOf(ctx context.Context) core.Caller {
	if c, ok := core.CallerFrom(ctx); ok {
		return c
	}
	return systemCaller
}

func isPrivileged(c core.Caller) bool {
	return c.IsRoot || c.Kind == core.CallerSystem || c.Kind == core.CallerHost
}

// audit records an action. A failing audit log never fails the action, but
// the failure is logged.
func (s *Service) audit(ctx context.Context, actor, action, target string, detail map[string]any) {
	raw, err := json.Marshal(detail)
	if err != nil {
		raw = json.RawMessage(`{}`)
	}
	err = s.d.Ports.Audit.Append(ctx, core.AuditEntry{Actor: actor, Action: action, Target: target, Detail: raw})
	if err != nil {
		s.d.Log.WarnContext(ctx, "audit append failed", "action", action, "error", err)
	}
}

// viewerOf says which channels and messages the caller may read.
func (s *Service) viewerOf(ctx context.Context, c core.Caller) domain.Viewer {
	switch c.Kind {
	case core.CallerAgent:
		if c.IsRoot {
			return domain.Viewer{All: true}
		}
		root, err := s.d.Ports.Agents.TeamRoot(ctx, c.Name)
		if err != nil {
			root = ""
		}
		return domain.Viewer{Name: c.Name, TeamRoot: root, IsBelow: s.isBelow(ctx, c.Name)}
	case core.CallerPlugin:
		return domain.Viewer{}
	default:
		return domain.Viewer{All: true}
	}
}

func (s *Service) isBelow(ctx context.Context, ancestor string) func(string) bool {
	return func(name string) bool {
		below, err := s.d.Ports.Agents.IsBelow(ctx, name, ancestor)
		return err == nil && below
	}
}

// access answers whether a viewer may read a message. It remembers which
// channels the viewer may see, because a channel never changes its kind.
// One access belongs to one goroutine.
type access struct {
	s      *Service
	viewer domain.Viewer
	seen   map[string]bool
}

func (s *Service) accessFor(v domain.Viewer) *access {
	return &access{s: s, viewer: v, seen: map[string]bool{}}
}

func (a *access) canSee(ctx context.Context, m domain.Message) bool {
	if m.IsDirect() {
		return a.viewer.CanSeeDirect(m)
	}
	if ok, known := a.seen[m.Channel]; known {
		return ok
	}
	c, err := a.s.d.Channels.Get(ctx, m.Channel)
	switch {
	case err == nil:
		a.seen[m.Channel] = a.viewer.CanSeeChannel(c)
	case errors.Is(err, core.ErrNotFound):
		a.seen[m.Channel] = false
	default:
		// A failed read hides the message this time and is not remembered.
		a.s.d.Log.WarnContext(ctx, "read channel failed", "channel", m.Channel, "error", err)
		return false
	}
	return a.seen[m.Channel]
}

// clampLimit applies the default and the maximum page size.
func clampLimit(n int) int {
	switch {
	case n < 1:
		return defaultLimit
	case n > maxLimit:
		return maxLimit
	}
	return n
}
