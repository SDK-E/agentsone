// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"fmt"
	"time"

	"github.com/SDK-E/agentsone/internal/agents/supervisor/domain"
	"github.com/SDK-E/agentsone/internal/contract/common"
	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/runners/sdk/protocol"
)

// Credential renewal on protocol 1.1 (PROTOCOL.md credentials, rule K1).
const (
	// renewAhead is the least time before expiry an update goes out.
	renewAhead = time.Minute
	// renewRetry is the wait after a renewal that could not be sent.
	renewRetry = 30 * time.Second
)

// Credential renewal log lines and audit action. The error text never holds
// token text.
const (
	LogCredentialsFailed    = "credentials renewal failed: the runner keeps its tokens, retrying every 30s"
	LogCredentialsRecovered = "credentials renewal works again"
	ActionCredentialsFailed = "agent.runner.credentials_failed"
)

// issued is one token the runner holds.
type issued struct {
	value     string
	issuedAt  time.Time
	expiresAt time.Time
}

// due is when the token should be replaced: two thirds into its life, and
// never later than renewAhead before it expires. A token that lives only
// seconds is replaced half way, so it is renewed once per life and not on
// every tick.
func (t issued) due() time.Time {
	life := t.expiresAt.Sub(t.issuedAt)
	at := t.issuedAt.Add(life * 2 / 3)
	if latest := t.expiresAt.Add(-renewAhead); latest.Before(at) {
		at = latest
	}
	if earliest := t.issuedAt.Add(life / 2); at.Before(earliest) {
		at = earliest
	}
	return at
}

// credentials are the tokens of one 1.1 connection. The supervisor's lock
// guards them.
type credentials struct {
	gateway, mcp issued
	// retryAt holds the next try after a renewal that could not be sent.
	retryAt time.Time
	// failing is set once a renewal failed and cleared by the next one that
	// is sent, so a failing spell is reported once.
	failing bool
}

// next is when the next update goes out.
func (c *credentials) next() time.Time {
	at := c.gateway.due()
	if m := c.mcp.due(); m.Before(at) {
		at = m
	}
	if c.retryAt.After(at) {
		return c.retryAt
	}
	return at
}

// renewCredentials sends credentials.update to every ready 1.1 runner whose
// tokens are due (rules K1, D2 to D6). The gateway token is fresh every
// time; the MCP token is resent unchanged until it is due itself. When a
// token cannot be issued nothing is sent, the runner keeps its tokens, and
// the next try is 30 seconds later.
func (s *Supervisor) renewCredentials(ctx context.Context, now time.Time) {
	type job struct {
		h     *Handler
		creds credentials
	}
	var jobs []job
	s.mu.Lock()
	for _, r := range s.runners {
		h := r.conn
		if h == nil || h.creds == nil || !r.session.Ready() || now.Before(h.creds.next()) {
			continue
		}
		jobs = append(jobs, job{h, *h.creds})
		h.creds.retryAt = now.Add(renewRetry) // claimed: no second send meanwhile
	}
	s.mu.Unlock()
	for _, j := range jobs {
		next, err := s.renew(ctx, j.h, j.creds, now)
		if err != nil {
			// One line and one audit record per failing spell, not one per retry.
			s.mu.Lock()
			first := j.h.creds != nil && !j.h.creds.failing
			if first {
				j.h.creds.failing = true
			}
			s.mu.Unlock()
			if first {
				s.log().Error(LogCredentialsFailed, "agent", j.h.agent, "err", err)
				s.audit(ctx, ActionCredentialsFailed, "agent:"+j.h.agent, map[string]any{"error": err.Error()})
			}
			continue
		}
		s.mu.Lock()
		recovered := j.creds.failing
		if j.h.creds != nil {
			*j.h.creds = next
		}
		s.mu.Unlock()
		if recovered {
			s.log().Info(LogCredentialsRecovered, "agent", j.h.agent)
		}
	}
}

// renew issues the tokens and sends one update. Errors never carry token
// text.
func (s *Supervisor) renew(ctx context.Context, h *Handler, c credentials, now time.Time) (credentials, error) {
	gw, err := s.gatewayToken(ctx, h.agent)
	if err != nil {
		return credentials{}, err
	}
	next := credentials{gateway: issued{value: gw, issuedAt: now, expiresAt: now.Add(s.d.Config.GatewayTokenTTL)}, mcp: c.mcp}
	if !now.Before(c.mcp.due()) {
		tok, err := s.issueMCP(ctx, h.agent)
		if err != nil {
			return credentials{}, err
		}
		next.mcp = issued{value: tok.Value, issuedAt: now, expiresAt: tok.Expires}
	}
	params := protocol.CredentialsUpdateParams{
		GatewayToken: protocol.ScopedToken(next.gateway.value), GatewayTokenExpiresAt: common.Timestamp(next.gateway.expiresAt.UTC()),
		McpToken: protocol.ScopedToken(next.mcp.value), McpTokenExpiresAt: common.Timestamp(next.mcp.expiresAt.UTC()),
	}
	if err := h.notify(ctx, protocol.MethodCredentialsUpdate, params); err != nil {
		return credentials{}, fmt.Errorf("send credentials.update: %w", err)
	}
	return next, nil
}

// issueMCP issues the agent's machine MCP token.
func (s *Supervisor) issueMCP(ctx context.Context, agent string) (core.Token, error) {
	a, err := s.d.Ports.Agents.GetAgent(ctx, agent)
	if err != nil {
		return core.Token{}, fmt.Errorf("mcp token for %s: %w", agent, err)
	}
	caller := core.Caller{Kind: core.CallerAgent, Name: agent, UID: a.UID, IsRoot: a.IsRoot, Scopes: []string{MCPScope}}
	tok, err := s.d.Ports.Tokens.Issue(ctx, caller, []string{MCPScope}, s.d.Config.MCPTokenTTL)
	if err != nil {
		return core.Token{}, fmt.Errorf("mcp token for %s: %w", agent, err)
	}
	return tok, nil
}

// welcomeCredentials records the welcome's tokens on a connection that gets
// updates.
func welcomeCredentials(version string, now time.Time, gw string, gwTTL time.Duration, mcp core.Token) *credentials {
	if !domain.CredentialsUpdates(version) {
		return nil
	}
	return &credentials{
		gateway: issued{value: gw, issuedAt: now, expiresAt: now.Add(gwTTL)},
		mcp:     issued{value: mcp.Value, issuedAt: now, expiresAt: mcp.Expires},
	}
}
