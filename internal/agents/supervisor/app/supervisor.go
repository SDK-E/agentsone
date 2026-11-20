// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/SDK-E/agentsone/internal/agents/supervisor/domain"
	"github.com/SDK-E/agentsone/internal/contract/common"
	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/runners/sdk/protocol"
)

// BudgetRestored is the event type that resumes a turn paused for budget.
const BudgetRestored = "agentsone.agents.budget.restored"

// Audit actions written by the supervisor.
const (
	ActionProvision        = "agent.runner.provision"
	ActionReconfigure      = "agent.runner.reconfigure"
	ActionRetire           = "agent.runner.retire"
	ActionTurnFault        = "agent.turn.fault"
	ActionHeartbeatTimeout = "agent.runner.heartbeat_timeout"
	ActionRunnerError      = "agent.runner.error"
	ActionFreeze           = "agent.freeze"
	ActionRelease          = "agent.release"
)

// Supervisor runs one runner per agent. It implements core.Waker and
// core.Freezer.
type Supervisor struct {
	d Deps

	mu      sync.Mutex
	runners map[string]*runner
	freezes []core.FreezeScope
	// wall is the network wall's last answer, checked at wallAt; until the
	// first check it is wallUnchecked. wallMarked are the agents shown as
	// failed because of it.
	wall        domain.NetWall
	wallAt      time.Time
	wallChecked bool
	wallMarked  map[string]bool
	// refused is the last wake each agent's runner refused, while it shows.
	refused map[string]refusal
	// resumed is the paused turn each agent was last resumed for, until its
	// budget runs out again.
	resumed map[string]string
	// resumeRetry holds back the next resume of an agent whose last resume
	// wake failed.
	resumeRetry map[string]resumeBackoff
}

var (
	_ core.Waker   = (*Supervisor)(nil)
	_ core.Freezer = (*Supervisor)(nil)
)

type runner struct {
	session *domain.Session
	conn    *Handler // the connection that finished hello, or nil
	pending []wakeRequest
}

type wakeRequest struct {
	reason  core.WakeReason
	payload json.RawMessage
}

// New builds a supervisor.
func New(d Deps) *Supervisor {
	return &Supervisor{d: d, runners: map[string]*runner{}, wall: wallUnchecked, wallMarked: map[string]bool{}, refused: map[string]refusal{}, resumed: map[string]string{}, resumeRetry: map[string]resumeBackoff{}}
}

func (s *Supervisor) runnerLocked(name string) *runner {
	r, ok := s.runners[name]
	if !ok {
		r = &runner{session: domain.NewSession(name)}
		s.runners[name] = r
	}
	return r
}

// LimitMode is the agent's limits status for the API: hard when a cgroup
// holds them, soft when only process resource limits apply, none for root
// agents, which run as root and can change their own limits within the
// container's ceiling. Only custom agents are held.
func (s *Supervisor) LimitMode(name string) domain.LimitMode { return s.d.Launcher.Mode(name) }

// SessionState returns the session state of agent name.
func (s *Supervisor) SessionState(name string) domain.State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.runnerLocked(name).session.State()
}

func (s *Supervisor) audit(ctx context.Context, action, target string, detail any) {
	raw, _ := json.Marshal(detail)
	_ = s.d.Ports.Audit.Append(ctx, core.AuditEntry{Actor: "system:supervisor", Action: action, Target: target, Detail: raw})
}

// Provision prepares agent a to run: for a custom agent its Linux user and
// home, then how its runner starts. It does not start the runner. It is
// idempotent: a custom agent's user and group are (re)created with exactly
// a.UID as uid and gid (the society context allocates and stores it; the
// system's user database may be reset on every start). limits nil
// means DefaultCustomLimits for custom agents and none for root agents. It
// returns the uid the runner runs as.
func (s *Supervisor) Provision(ctx context.Context, a core.Agent, limits *domain.Limits) (int, error) {
	uid, err := s.install(ctx, a, limits)
	if err != nil {
		return 0, err
	}
	s.audit(ctx, ActionProvision, "agent:"+a.Name, map[string]any{"uid": uid, "runner": a.Runner, "root": a.IsRoot})
	return uid, nil
}

func (s *Supervisor) install(ctx context.Context, a core.Agent, limits *domain.Limits) (int, error) {
	if err := domain.ValidateName(a.Name); err != nil {
		return 0, fmt.Errorf("provision: %w: %w", core.ErrInvalid, err)
	}
	account := domain.AccountFor(a.Name, a.IsRoot).WithUID(a.UID)
	uid := 0
	if account.IsRoot {
		if err := s.d.Accounts.EnsureRootHome(ctx, account); err != nil {
			return 0, fmt.Errorf("provision %s: home: %w", a.Name, err)
		}
	}
	if account.Create {
		if account.UID <= 0 {
			return 0, fmt.Errorf("provision %s: custom agent has no uid: %w", a.Name, core.ErrInvalid)
		}
		var err error
		if uid, err = s.d.Accounts.Ensure(ctx, account); err != nil {
			return 0, fmt.Errorf("provision %s: account: %w", a.Name, err)
		}
	}
	a.UID, a.Home = uid, account.Home
	adapter, err := s.d.Ports.Runners.Runner(ctx, a.Runner)
	if err != nil {
		return 0, fmt.Errorf("provision %s: runner %q: %w", a.Name, a.Runner, err)
	}
	spec, err := adapter.Exec(ctx, a, domain.SocketPath(a.Name))
	if err != nil {
		return 0, fmt.Errorf("provision %s: exec: %w", a.Name, err)
	}
	l := domain.Limits{}
	switch {
	case limits != nil:
		l = *limits
	case !a.IsRoot:
		l = domain.DefaultCustomLimits
	}
	unit := domain.UnitSpec{Agent: a.Name, Account: account, Limits: l,
		Exec: domain.Exec{Path: spec.Path, Args: spec.Args, Env: spec.Env, Dir: spec.Dir}}
	if err := unit.Validate(); err != nil {
		return 0, fmt.Errorf("provision %s: %w: %w", a.Name, core.ErrInvalid, err)
	}
	if err := s.d.Launcher.Install(ctx, unit); err != nil {
		return 0, fmt.Errorf("provision %s: install: %w", a.Name, err)
	}
	return uid, nil
}

// Reconfigure rewrites the unit after an edit of kind, runner or limits,
// and restarts a running runner so the change applies.
func (s *Supervisor) Reconfigure(ctx context.Context, a core.Agent, limits *domain.Limits) error {
	if _, err := s.install(ctx, a, limits); err != nil {
		return err
	}
	s.mu.Lock()
	r := s.runnerLocked(a.Name)
	running := r.session.State() != domain.StateStopped && r.session.State() != domain.StateSleeping
	if running {
		s.dropConnLocked(r)
		r.session.Fail()
		_ = r.session.Start(s.d.Clock.Now())
	}
	s.mu.Unlock()
	if running {
		if err := s.d.Launcher.Restart(ctx, a.Name); err != nil {
			return fmt.Errorf("reconfigure %s: restart: %w", a.Name, err)
		}
	}
	s.audit(ctx, ActionReconfigure, "agent:"+a.Name, map[string]any{"restarted": running})
	return nil
}

// Retire stops the agent's runner and removes its unit and socket. A custom
// agent's user is locked, not deleted, and its home is kept as the archive.
func (s *Supervisor) Retire(ctx context.Context, name string) error {
	if err := domain.ValidateName(name); err != nil {
		return fmt.Errorf("retire: %w: %w", core.ErrInvalid, err)
	}
	a, err := s.d.Ports.Agents.GetAgent(ctx, name)
	if err != nil {
		return fmt.Errorf("retire %s: %w", name, err)
	}
	s.mu.Lock()
	if r, ok := s.runners[name]; ok {
		s.dropConnLocked(r)
	}
	delete(s.runners, name)
	s.mu.Unlock()
	if err := s.d.Launcher.Stop(ctx, name); err != nil {
		return fmt.Errorf("retire %s: stop: %w", name, err)
	}
	if err := s.d.Launcher.Remove(ctx, name); err != nil {
		return fmt.Errorf("retire %s: remove: %w", name, err)
	}
	if !a.IsRoot {
		if err := s.d.Accounts.Lock(ctx, domain.AccountFor(name, false)); err != nil {
			return fmt.Errorf("retire %s: lock account: %w", name, err)
		}
	}
	s.audit(ctx, ActionRetire, "agent:"+name, map[string]any{"homeKept": !a.IsRoot})
	return nil
}

// Wake starts a turn for agent name. A stopped or sleeping agent's runner
// is started first and the wake is sent once it is ready. A frozen agent is
// refused with core.ErrFrozen; while the network wall does not hold, every
// agent is refused with ErrNetworkWall and core.ErrUnavailable.
func (s *Supervisor) Wake(ctx context.Context, name string, reason core.WakeReason, payload json.RawMessage) error {
	if err := domain.ValidateName(name); err != nil {
		return fmt.Errorf("wake: %w: %w", core.ErrInvalid, err)
	}
	if w, ok := s.wallRefusal(); !ok {
		s.markWallBroken(ctx, name, w, false)
		return fmt.Errorf("wake %s: %w: %s: %w", name, ErrNetworkWall, w.Reason, core.ErrUnavailable)
	}
	frozen, err := s.Frozen(ctx, name)
	if err != nil {
		return err
	}
	s.mu.Lock()
	r := s.runnerLocked(name)
	req := wakeRequest{reason: reason, payload: payload}
	switch domain.PlanWake(frozen, r.session.State()) {
	case domain.WakeRefuse:
		s.mu.Unlock()
		return fmt.Errorf("wake %s: %w", name, core.ErrFrozen)
	case domain.WakeStart:
		_ = r.session.Start(s.d.Clock.Now())
		r.pending = append(r.pending, req)
		s.mu.Unlock()
		// A runner without the model gateway cannot work: do not start it.
		if _, err := s.gatewayToken(ctx, name); err != nil {
			s.mu.Lock()
			r.session.Fail()
			r.pending = nil
			s.mu.Unlock()
			s.audit(ctx, ActionRunnerError, "agent:"+name, map[string]any{"state": "not started", "message": err.Error()})
			s.runnerFailed(ctx, name, gatewayReason(err))
			return fmt.Errorf("wake %s: %w", name, err)
		}
		if err := s.d.Launcher.Start(ctx, name); err != nil {
			s.mu.Lock()
			r.session.Fail()
			r.pending = nil
			s.mu.Unlock()
			s.runnerFailed(ctx, name, ReasonStartFailed)
			return fmt.Errorf("wake %s: start: %w", name, err)
		}
		return nil
	case domain.WakeHold:
		r.pending = append(r.pending, req)
		s.mu.Unlock()
		return nil
	default:
		h := r.conn
		turn := s.turnFor(r.session, req)
		s.mu.Unlock()
		err := h.sendWake(ctx, req, turn)
		s.wakeSent(ctx, name, req, err)
		return err
	}
}

// turnFor picks the turn id: the paused turn for a budget resume (rule R2),
// otherwise a new trn_<ULID>.
func (s *Supervisor) turnFor(sess *domain.Session, req wakeRequest) string {
	if req.reason == core.WakeEvent && sess.PausedTurn() != "" {
		var ev struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(req.payload, &ev) == nil && ev.Type == BudgetRestored {
			return sess.PausedTurn()
		}
	}
	return s.d.IDs.New("trn")
}

// Sleep puts agent name to rest: with compact, the runner writes its
// summary first, which the next welcome hands back. Then the runner is shut
// down and its unit stopped, so a sleeping agent costs nothing.
func (s *Supervisor) Sleep(ctx context.Context, name string, compact bool) error {
	s.mu.Lock()
	r := s.runnerLocked(name)
	h := r.conn
	ready := r.session.Ready()
	turn := r.session.Turn()
	paused := r.session.PausedTurn()
	s.mu.Unlock()
	if h != nil && ready {
		if turn != "" {
			_ = h.call(ctx, protocol.MethodCancel, protocol.CancelParams{TurnID: turnID(turn), Reason: "sleep"}, nil)
		}
		if compact {
			var res protocol.SleepResult
			if err := h.call(ctx, protocol.MethodSleep, protocol.SleepParams{Reason: "idle", MaxSummaryChars: s.d.Config.MaxSummaryChars}, &res); err != nil {
				return fmt.Errorf("sleep %s: %w", name, err)
			}
			sum := Summary{Text: res.Summary, CompactedAt: s.d.Clock.Now(), PausedTurnID: paused}
			if err := s.d.Summaries.Save(ctx, name, sum); err != nil {
				return fmt.Errorf("sleep %s: save summary: %w", name, err)
			}
		}
		grace := int(s.d.Config.ShutdownGrace.Milliseconds())
		_ = h.call(ctx, protocol.MethodShutdown, protocol.ShutdownParams{Reason: "sleep", GraceMs: grace}, nil)
	}
	s.mu.Lock()
	s.dropConnLocked(r)
	r.session.Stop()
	r.pending = nil
	s.mu.Unlock()
	if err := s.d.Launcher.Stop(ctx, name); err != nil {
		return fmt.Errorf("sleep %s: stop: %w", name, err)
	}
	return nil
}

// dropConnLocked forgets the connection and closes it. Its Closed callback
// then finds it is no longer current and changes nothing.
func (s *Supervisor) dropConnLocked(r *runner) {
	if r.conn != nil {
		h := r.conn
		r.conn = nil
		go func() { _ = h.closeConn() }()
	}
}

// Tick restarts every runner that has been silent for the heartbeat
// timeout (rule B2): it records -32003 in the audit trail, closes the
// connection and restarts the unit. Every WallRecheck it checks the network
// wall again. It also sends credentials.update to 1.1 runners whose tokens
// are due. The module calls it on a timer; tests call it after moving the
// fake clock.
func (s *Supervisor) Tick(ctx context.Context) error {
	now := s.d.Clock.Now()
	if s.wallDue(now) {
		s.CheckWall(ctx)
	}
	var expired []string
	s.mu.Lock()
	for name, r := range s.runners {
		if r.session.Expired(now, s.d.Config.HeartbeatTimeout) {
			expired = append(expired, name)
			s.dropConnLocked(r)
			r.session.Fail()
			_ = r.session.Start(now)
		}
	}
	s.mu.Unlock()
	s.renewCredentials(ctx, now)
	s.resumeBudget(ctx)
	var errs []error
	for _, name := range expired {
		s.audit(ctx, ActionHeartbeatTimeout, "agent:"+name, map[string]any{"code": -32003})
		if err := s.d.Launcher.Restart(ctx, name); err != nil {
			errs = append(errs, fmt.Errorf("restart %s: %w", name, err))
		}
	}
	return errors.Join(errs...)
}

func turnID(s string) common.TurnID { return common.TurnID(s) }
