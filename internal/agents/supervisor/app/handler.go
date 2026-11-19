// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/SDK-E/agentsone/internal/agents/supervisor/domain"
	"github.com/SDK-E/agentsone/internal/contract/common"
	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/runners/sdk/protocol"
)

// MCPScope is the token scope a runner's MCP token carries.
const MCPScope = "mcp"

// Handler serves one runner connection of one agent. Build it with Connect,
// hand it to the protocol server, then Attach the server's connection.
type Handler struct {
	s     *Supervisor
	agent string

	once     sync.Once
	attached chan struct{}
	conn     RunnerConn
	log      *slog.Logger
}

// Connect returns the handler for a new connection on agent name's socket.
func (s *Supervisor) Connect(name string) *Handler {
	return &Handler{s: s, agent: name, attached: make(chan struct{}), log: slog.Default().With("agent", name)}
}

// Attach gives the handler its connection. Calls wait until it is attached.
func (h *Handler) Attach(c RunnerConn) {
	h.once.Do(func() {
		h.conn = c
		close(h.attached)
	})
}

func (h *Handler) call(ctx context.Context, method string, params, result any) error {
	select {
	case <-h.attached:
	case <-ctx.Done():
		return ctx.Err()
	}
	return h.conn.Call(ctx, method, params, result)
}

func (h *Handler) closeConn() error {
	<-h.attached
	return h.conn.Close()
}

func (h *Handler) sendWake(ctx context.Context, req wakeRequest, turn string) error {
	params := map[string]any{"reason": string(req.reason), "turnId": turn, "payload": req.payload}
	if err := h.call(ctx, protocol.MethodWake, params, nil); err != nil {
		return fmt.Errorf("wake %s: %w", h.agent, err)
	}
	return nil
}

// current reports whether h is the agent's live connection; with the
// lock held it returns the runner.
func (h *Handler) currentLocked() (*runner, bool) {
	r, ok := h.s.runners[h.agent]
	return r, ok && r.conn == h
}

// Hello builds the welcome (PROTOCOL.md handshake step 2). A second
// connection while one is live is refused.
func (h *Handler) Hello(ctx context.Context, version string, p protocol.HelloParams) (protocol.HelloResult, error) {
	s := h.s
	now := s.d.Clock.Now()
	s.mu.Lock()
	r := s.runnerLocked(h.agent)
	if r.conn != nil && r.conn != h {
		s.mu.Unlock()
		return protocol.HelloResult{}, fmt.Errorf("second connection for %s: %w", h.agent, domain.ErrNotPermitted)
	}
	if r.session.State() != domain.StateStarting {
		// The unit restarted on its own (Restart=on-failure): adopt it.
		r.session.Fail()
		_ = r.session.Start(now)
	}
	s.mu.Unlock()
	h.log.Info("runner connected", "runner", p.Runner.Name, "version", string(p.Runner.Version), "protocol", version)

	id, err := s.d.Identity.Identity(ctx, h.agent)
	if err != nil {
		return protocol.HelloResult{}, fmt.Errorf("identity of %s: %w", h.agent, err)
	}
	sum, err := s.d.Summaries.Load(ctx, h.agent)
	if err != nil {
		return protocol.HelloResult{}, fmt.Errorf("summary of %s: %w", h.agent, err)
	}
	caller := core.Caller{Kind: core.CallerAgent, Name: h.agent, UID: id.Agent.UID, IsRoot: id.Agent.IsRoot, Scopes: []string{MCPScope}}
	mcp, err := s.d.Ports.Tokens.Issue(ctx, caller, []string{MCPScope}, s.d.Config.MCPTokenTTL)
	if err != nil {
		return protocol.HelloResult{}, fmt.Errorf("mcp token for %s: %w", h.agent, err)
	}
	gw, err := s.gatewayToken(ctx, h.agent)
	if err != nil {
		s.runnerFailed(ctx, h.agent, gatewayReason(err))
		return protocol.HelloResult{}, err
	}

	s.mu.Lock()
	if err := r.session.Welcome(now); err != nil {
		s.mu.Unlock()
		return protocol.HelloResult{}, err
	}
	r.conn = h
	paused := r.session.PausedTurn()
	s.mu.Unlock()
	if paused == "" {
		paused = sum.PausedTurnID
	}
	return welcome(version, id, mcp, gw, sum, paused, s.d.Config), nil
}

func welcome(version string, id AgentIdentity, mcp core.Token, gw string, sum Summary, paused string, cfg Config) protocol.HelloResult {
	goals := make([]common.OneLine, len(id.Goals))
	for i, g := range id.Goals {
		goals[i] = common.OneLine(g)
	}
	var manager protocol.AgentIdentityManager
	if id.Agent.Manager != "" {
		m := id.Agent.Manager
		manager = &m
	}
	var session protocol.SessionState
	if sum.Text != "" {
		text, at := sum.Text, common.Timestamp(sum.CompactedAt.UTC())
		session.Summary, session.CompactedAt = &text, &at
	}
	if paused != "" {
		session.PausedTurnID = &paused
	}
	return protocol.HelloResult{
		ProtocolVersion: protocol.ProtocolVersion(version),
		Agent: protocol.AgentIdentity{
			Name: common.AgentName(id.Agent.Name), Kind: protocol.AgentIdentityKind(id.Agent.Kind),
			IsRoot: id.Agent.IsRoot, Uid: id.Agent.UID, Team: common.AgentName(id.Team), Manager: manager,
			OneLine: common.OneLine(id.OneLine), Charter: common.Markdown(id.Charter), Goals: goals,
		},
		Mcp: protocol.McpEndpointHttp{Transport: "streamable-http", Url: cfg.MCPURL,
			Token: protocol.ScopedToken(mcp.Value), TokenExpiresAt: common.Timestamp(mcp.Expires.UTC())},
		Model: protocol.ModelGateway{BaseUrl: cfg.GatewayURL, Name: "autoagents/one", Token: protocol.ScopedToken(gw),
			Weights: []protocol.Weight{"quick", "normal", "deep"}, DefaultWeight: "normal"},
		Session: session,
		Config: protocol.RunnerConfig{
			HeartbeatIntervalMs: int(cfg.HeartbeatInterval / time.Millisecond),
			HeartbeatTimeoutMs:  int(cfg.HeartbeatTimeout / time.Millisecond),
			MaxLineBytes:        cfg.MaxLineBytes, Runner: protocol.RunnerConfigRunner{},
		},
	}
}

// Heard moves the heartbeat deadline: any line is a sign of life.
func (h *Handler) Heard() {
	h.s.mu.Lock()
	defer h.s.mu.Unlock()
	if r, ok := h.currentLocked(); ok {
		r.session.Heard(h.s.d.Clock.Now())
	}
}

// Status applies a status notification. The first idle releases held
// wakes; a turn that ended without profile.update is recorded as a fault (P1).
func (h *Handler) Status(ctx context.Context, p protocol.StatusParams) error {
	st := domain.RunnerStatus{State: string(p.State)}
	if p.TurnID != nil {
		st.TurnID = string(*p.TurnID)
	}
	if p.PausedTurnID != nil {
		st.PausedTurnID = string(*p.PausedTurnID)
	}
	s := h.s
	s.mu.Lock()
	r, ok := h.currentLocked()
	if !ok {
		s.mu.Unlock()
		return domain.ErrOutOfOrder
	}
	welcomed := r.session.State() == domain.StateWelcomed
	out, err := r.session.Status(st)
	ready := err == nil && welcomed && r.session.State() == domain.StateIdle
	var held []wakeRequest
	var turns []string
	if err == nil && r.session.State() == domain.StateIdle && len(r.pending) > 0 {
		held, r.pending = r.pending, nil
		for _, req := range held {
			turns = append(turns, s.turnFor(r.session, req))
		}
	}
	s.mu.Unlock()
	if err != nil {
		return err
	}
	if out.MissingProfile {
		s.audit(ctx, ActionTurnFault, "agent:"+h.agent, map[string]any{"turnId": out.EndedTurn, "fault": "no profile.update"})
	}
	if ready {
		s.runnerRunning(ctx, h.agent)
	}
	if out.Failed {
		s.audit(ctx, ActionRunnerError, "agent:"+h.agent, map[string]any{"turnId": out.EndedTurn, "state": "error"})
		s.runnerFailed(ctx, h.agent, ReasonRunnerError)
	}
	if len(held) > 0 {
		// Calls wait for answers that this read loop delivers, so send from
		// another goroutine.
		go h.flush(context.WithoutCancel(ctx), held, turns)
	}
	return nil
}

func (h *Handler) flush(ctx context.Context, held []wakeRequest, turns []string) {
	for i, req := range held {
		if err := h.sendWake(ctx, req, turns[i]); err != nil {
			h.log.Warn("held wake not delivered", "err", err)
			return
		}
	}
}

func (h *Handler) inTurn(turn string) error {
	h.s.mu.Lock()
	defer h.s.mu.Unlock()
	r, ok := h.currentLocked()
	if !ok || !r.session.InTurn(turn) {
		return fmt.Errorf("turn %q: %w", turn, domain.ErrUnknownTurn)
	}
	return nil
}

// Output passes a chunk of the running turn to the chat store and stream.
func (h *Handler) Output(ctx context.Context, p protocol.OutputParams) error {
	if err := h.inTurn(string(p.TurnID)); err != nil {
		return err
	}
	c := OutputChunk{TurnID: string(p.TurnID), Index: p.Chunk, Text: p.Text, Final: p.Final}
	if p.ChatID != nil {
		c.ChatID = string(*p.ChatID)
	}
	return h.s.d.Output.Output(ctx, h.agent, c)
}

// Usage records model use for the soft budget.
func (h *Handler) Usage(ctx context.Context, p protocol.UsageParams) error {
	if err := h.inTurn(string(p.TurnID)); err != nil {
		return err
	}
	u := Usage{TurnID: string(p.TurnID), Model: p.Model, Weight: string(p.Weight), TokensIn: p.TokensIn,
		TokensOut: p.TokensOut, CostMicroUSD: p.CostMicroUsd}
	if p.CacheReadTokens != nil {
		u.CacheReadTokens = *p.CacheReadTokens
	}
	return h.s.d.Usage.RecordUsage(ctx, h.agent, u)
}

// ProfileUpdate stores the agent's one-line in the live list (rule P1).
func (h *Handler) ProfileUpdate(ctx context.Context, p protocol.ProfileUpdateParams) error {
	h.s.mu.Lock()
	r, ok := h.currentLocked()
	var err error
	if !ok {
		err = domain.ErrUnknownTurn
	} else {
		err = r.session.Profile(string(p.TurnID))
	}
	h.s.mu.Unlock()
	if err != nil {
		return err
	}
	return h.s.d.Ports.Live.UpdateLine(ctx, h.agent, string(p.Line))
}

// Log writes a runner diagnostic to the daemon log.
func (h *Handler) Log(ctx context.Context, p protocol.LogParams) {
	level := map[protocol.LogParamsLevel]slog.Level{"debug": slog.LevelDebug, "warn": slog.LevelWarn, "error": slog.LevelError}[p.Level]
	h.log.Log(ctx, level, p.Message, "source", "runner")
}

// Error records a runner failure; fatal ones go to the audit trail.
func (h *Handler) Error(ctx context.Context, p protocol.ErrorParams) {
	h.log.Warn("runner error", "code", int(p.Code), "message", p.Message, "fatal", p.Fatal)
	if p.Fatal {
		h.s.audit(ctx, ActionRunnerError, "agent:"+h.agent, map[string]any{"code": int(p.Code), "message": p.Message})
	}
}

// BusSend sends a bus message from the agent to another agent.
func (h *Handler) BusSend(ctx context.Context, p protocol.BusSendParams) (protocol.BusSendResult, error) {
	if err := h.inTurn(string(p.TurnID)); err != nil {
		return protocol.BusSendResult{}, err
	}
	if _, err := h.s.d.Ports.Agents.GetAgent(ctx, string(p.To)); err != nil {
		if errors.Is(err, core.ErrNotFound) {
			return protocol.BusSendResult{}, fmt.Errorf("bus.send to %s: %w", p.To, domain.ErrUnknownRecipient)
		}
		return protocol.BusSendResult{}, err
	}
	body := strings.TrimSpace(string(p.Subject)) + "\n\n" + string(p.Body)
	m, err := h.s.d.Ports.Bus.Send(ctx, core.BusMessage{From: "agent:" + h.agent, To: "agent:" + string(p.To), Body: body})
	if err != nil {
		return protocol.BusSendResult{}, err
	}
	return protocol.BusSendResult{MessageID: common.MessageID(m.ID)}, nil
}

// TaskDone ends a task the agent was assigned.
func (h *Handler) TaskDone(ctx context.Context, p protocol.TaskDoneParams) error {
	if err := h.inTurn(string(p.TurnID)); err != nil {
		return err
	}
	tasks := h.s.d.Ports.Tasks
	t, err := tasks.GetTask(ctx, string(p.TaskID))
	if errors.Is(err, core.ErrNotFound) {
		return fmt.Errorf("task.done %s: %w", p.TaskID, domain.ErrUnknownTask)
	}
	if err != nil {
		return err
	}
	if t.Assignee != h.agent {
		return fmt.Errorf("task.done %s by %s: %w", p.TaskID, h.agent, domain.ErrNotPermitted)
	}
	status := map[protocol.TaskDoneParamsOutcome]string{"done": core.TaskDone, "failed": core.TaskFailed, "declined": core.TaskCancelled}[p.Outcome]
	if status == "" {
		return fmt.Errorf("task.done outcome %q: %w", p.Outcome, core.ErrInvalid)
	}
	result := string(p.Summary)
	if p.Details != nil {
		result += "\n\n" + string(*p.Details)
	}
	_, err = tasks.CompleteTask(ctx, t.ID, core.TaskOutcome{Status: status, Result: result})
	return err
}

// Closed marks the runner gone when this was its live connection.
func (h *Handler) Closed(err error) {
	if err != nil {
		h.log.Warn("runner connection ended", "err", err)
	}
	h.s.mu.Lock()
	defer h.s.mu.Unlock()
	if r, ok := h.currentLocked(); ok {
		r.conn = nil
		r.session.Stop()
	}
}
