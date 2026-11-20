// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/SDK-E/agentsone/internal/agents/domain"
	"github.com/SDK-E/agentsone/internal/core"
)

// Templates returns the installed agent templates. Every caller may read them.
func (s *Society) Templates(ctx context.Context) ([]core.AgentTemplate, error) {
	return s.ports.Templates.List(ctx)
}

// templateSeed is what a template gives a new agent beyond its spec.
type templateSeed struct {
	OneLine string   `json:"oneLine"`
	Goals   []string `json:"goals"`
	// Charter is the starting charter. Seeding the society makes the
	// founder's the society's first charter.
	Charter string `json:"charter"`
}

// applyTemplate fills spec from the named template. The template decides
// whether the agent is root, so the root rule holds for templates too.
func (s *Society) applyTemplate(ctx context.Context, spec domain.HireSpec) (domain.HireSpec, templateSeed, error) {
	if spec.Template == "" {
		return spec, templateSeed{}, nil
	}
	tpl, err := s.template(ctx, spec.Template)
	if err != nil {
		return domain.HireSpec{}, templateSeed{}, err
	}
	var seed templateSeed
	if len(tpl.Raw) > 0 {
		if err := json.Unmarshal(tpl.Raw, &seed); err != nil {
			return domain.HireSpec{}, templateSeed{}, fmt.Errorf("%w: template %s: %w", domain.ErrInvalidAgent, tpl.ID, err)
		}
	}
	spec.IsRoot = tpl.IsRoot
	spec.Template = tpl.ID
	return spec, seed, nil
}

// template reads an installed template; an unknown id is ErrInvalidAgent.
func (s *Society) template(ctx context.Context, id string) (core.AgentTemplate, error) {
	tpl, err := s.ports.Templates.Get(ctx, id)
	if errors.Is(err, core.ErrNotFound) {
		return core.AgentTemplate{}, fmt.Errorf("%w: no installed template %q", domain.ErrInvalidAgent, id)
	}
	if err != nil {
		return core.AgentTemplate{}, fmt.Errorf("read template %s: %w", id, err)
	}
	return tpl, nil
}

// seedFromTemplate writes the template's first one-line and goals for the
// newly hired agent. The one-line is the agent's own, so it is set
// directly; the goals are set by the hirer, as an agent above.
func (s *Society) seedFromTemplate(ctx context.Context, hirer string, chart domain.Chart, a domain.Agent, seed templateSeed) error {
	if seed.OneLine != "" {
		if err := s.ports.Live.UpdateLine(ctx, a.Name, seed.OneLine); err != nil {
			return fmt.Errorf("seed one-line of %s: %w", a.Name, err)
		}
	} else if err := s.ports.Live.UpdateLine(ctx, a.Name, StartingLine(a.Role)); err != nil {
		// The agent is hired already; a missing line is filled at the next start.
		s.log.WarnContext(ctx, "agents: a new agent has no starting one-line", "agent", a.Name, "error", err)
	}
	for _, title := range seed.Goals {
		g, err := domain.NewGoal(chart, hirer, domain.GoalSpec{Owner: a.Name, Title: title}, nil, s.ids.New("gol"), s.clock.Now())
		if err != nil {
			return fmt.Errorf("seed goal of %s: %w", a.Name, err)
		}
		if err := s.repo.CreateGoal(ctx, g); err != nil {
			return fmt.Errorf("store seed goal of %s: %w", a.Name, err)
		}
	}
	return nil
}

// noRoleLine is the one-line of a new agent that has no role.
const noRoleLine = "new"

// StartingLine is what an agent's one-line says before its first turn: its
// role on one line, cut to fit the live list, or "new" when it has none.
// The agent's own first line replaces it.
func StartingLine(role string) string {
	line := strings.Join(strings.Fields(role), " ")
	for len(line) > core.MaxLiveLine {
		_, size := utf8.DecodeLastRuneInString(line)
		line = line[:len(line)-size]
	}
	if line = strings.TrimSpace(line); line == "" {
		return noRoleLine
	}
	return line
}

// FillStartingLines gives every active agent that has no one-line yet its
// starting line. The daemon runs it at start, for agents hired before new
// agents got one. It stops at the first failure.
func (s *Society) FillStartingLines(ctx context.Context) error {
	chart, err := s.chart(ctx)
	if err != nil {
		return err
	}
	lines, err := s.ports.Live.Lines(ctx)
	if err != nil {
		return fmt.Errorf("read live list: %w", err)
	}
	has := make(map[string]bool, len(lines))
	for _, l := range lines {
		has[l.Agent] = true
	}
	for _, a := range chart.Agents() {
		if !a.Active() || has[a.Name] {
			continue
		}
		if err := s.ports.Live.UpdateLine(ctx, a.Name, StartingLine(a.Role)); err != nil {
			return fmt.Errorf("starting one-line of %s: %w", a.Name, err)
		}
	}
	return nil
}

// TokenRequest is the issueAgentToken input.
type TokenRequest struct {
	Label  string
	Scopes []string
	TTL    time.Duration
}

// IssuedToken is a token returned once to its issuer.
type IssuedToken struct {
	ID      string
	Value   string
	Subject string
	Label   string
	Scopes  []string
	Expires time.Time
	Created time.Time
}

const (
	maxTokenLabel = 80
	minTokenTTL   = time.Minute
	maxTokenTTL   = 30 * 24 * time.Hour
)

func (r TokenRequest) validate() error {
	switch n := utf8.RuneCountInString(r.Label); {
	case n < 1 || n > maxTokenLabel:
		return fmt.Errorf("token label has %d characters, want 1..%d: %w", n, maxTokenLabel, core.ErrInvalid)
	case len(r.Scopes) == 0:
		return fmt.Errorf("a token needs at least one scope: %w", core.ErrInvalid)
	case r.TTL < minTokenTTL || r.TTL > maxTokenTTL:
		return fmt.Errorf("token lifetime %s outside %s..%s: %w", r.TTL, minTokenTTL, maxTokenTTL, core.ErrInvalid)
	}
	return nil
}

// IssueToken issues a bearer token for agent, for tools that cannot use
// the socket. The agent itself, an agent above it or a human may issue
// one, and never with a scope the issuer does not hold.
func (s *Society) IssueToken(ctx context.Context, agent string, req TokenRequest) (IssuedToken, error) {
	c, ok := core.CallerFrom(ctx)
	if !ok {
		return IssuedToken{}, fmt.Errorf("no caller: %w", core.ErrUnauthorized)
	}
	if err := req.validate(); err != nil {
		return IssuedToken{}, err
	}
	chart, err := s.chart(ctx)
	if err != nil {
		return IssuedToken{}, err
	}
	a, err := chart.Get(agent)
	if err != nil {
		return IssuedToken{}, err
	}
	if c.Kind == core.CallerAgent && c.Name != agent {
		if err := chart.CanManage(c.Name, agent); err != nil {
			return IssuedToken{}, err
		}
	} else if c.Kind != core.CallerAgent && c.Kind != core.CallerHuman {
		return IssuedToken{}, fmt.Errorf("issue token for %s: %w", agent, core.ErrForbidden)
	}
	for _, scope := range req.Scopes {
		if !c.Has(scope) {
			return IssuedToken{}, fmt.Errorf("issuer does not hold scope %q: %w", scope, core.ErrForbidden)
		}
	}
	subject := core.Caller{Kind: core.CallerAgent, Name: a.Name, UID: a.UID, IsRoot: a.IsRoot}
	tok, err := s.ports.Tokens.Issue(ctx, subject, req.Scopes, req.TTL)
	if err != nil {
		return IssuedToken{}, fmt.Errorf("issue token for %s: %w", agent, err)
	}
	issued := IssuedToken{
		ID: tok.ID, Value: tok.Value, Subject: "agent:" + a.Name, Label: req.Label,
		Scopes: append([]string(nil), req.Scopes...), Expires: tok.Expires, Created: s.clock.Now(),
	}
	detail, err := json.Marshal(map[string]any{"id": issued.ID, "label": req.Label, "scopes": req.Scopes, "expires_at": tok.Expires})
	if err != nil {
		return IssuedToken{}, fmt.Errorf("encode token detail: %w", err)
	}
	if err := s.ports.Audit.Append(ctx, core.AuditEntry{Actor: string(c.Kind) + ":" + c.Name, Action: "token.issue", Target: "agent:" + a.Name, Detail: detail}); err != nil {
		return IssuedToken{}, fmt.Errorf("audit token.issue: %w", err)
	}
	return issued, nil
}
