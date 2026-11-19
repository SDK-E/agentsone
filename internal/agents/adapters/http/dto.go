// SPDX-License-Identifier: MIT

package httpadapter

import (
	"time"

	"github.com/SDK-E/agentsone/internal/agents/app"
	"github.com/SDK-E/agentsone/internal/agents/domain"
	"github.com/SDK-E/agentsone/internal/contract/api"
	"github.com/SDK-E/agentsone/internal/core"
)

// This file is the only place generated DTOs meet domain values, so a
// contract rename stays a change to this file alone.

// defaultCurrency names the split's currency before the host first pushes
// a budget.
const defaultCurrency = "USD"

func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func toAgent(a domain.Agent, team string, line *core.LiveLine) api.Agent {
	out := api.Agent{
		Name: a.Name, DisplayName: a.DisplayName, Kind: api.AgentKind(a.Kind), IsRoot: a.IsRoot,
		Runner: a.Runner, Manager: optional(a.Manager), Team: team, Role: a.Role, Description: a.Description,
		State: api.AgentState(a.State), Uid: max(0, a.UID), Home: a.Home, Unit: a.Unit(),
		Template: optional(a.Template), CreatedAt: a.CreatedAt, CreatedBy: a.CreatedBy, LastTurnAt: a.LastTurnAt,
	}
	if a.Limits != nil {
		out.Limits = &api.AgentLimits{CpuPercent: a.Limits.CPUPercent, MemoryMb: a.Limits.MemoryMB, Tasks: a.Limits.Tasks, Wall: api.WallSoft}
	}
	if line != nil {
		ol := toOneLine(*line)
		out.OneLine = &ol
	}
	return out
}

func toOneLine(l core.LiveLine) api.AgentOneLine {
	return api.AgentOneLine{Text: l.Line, UpdatedAt: l.Updated}
}

// limitsFrom fills the fields a request leaves out from base.
func limitsFrom(in *api.AgentLimitsInput, base domain.Limits) *domain.Limits {
	if in == nil {
		return nil
	}
	return &domain.Limits{
		CPUPercent: valueOr(in.CpuPercent, base.CPUPercent),
		MemoryMB:   valueOr(in.MemoryMb, base.MemoryMB),
		Tasks:      valueOr(in.Tasks, base.Tasks),
	}
}

func hireSpec(in api.AgentCreate) domain.HireSpec {
	return domain.HireSpec{
		Name: in.Name, DisplayName: in.DisplayName, Kind: domain.Kind(in.Kind), IsRoot: valueOr(in.IsRoot, false),
		Runner: in.Runner, Manager: valueOr(in.Manager, ""), Role: in.Role, Description: valueOr(in.Description, ""),
		Limits: limitsFrom(in.Limits, domain.DefaultLimits), Template: valueOr(in.Template, ""),
	}
}

func agentUpdate(in api.AgentUpdate, current domain.Agent) domain.Update {
	base := domain.DefaultLimits
	if current.Limits != nil {
		base = *current.Limits
	}
	u := domain.Update{
		DisplayName: in.DisplayName, Runner: in.Runner, Role: in.Role, Description: in.Description,
		Limits: limitsFrom(in.Limits, base),
	}
	if in.Kind != nil {
		k := domain.Kind(*in.Kind)
		u.Kind = &k
	}
	return u
}

func toChatMessage(m domain.ChatMessage) api.ChatMessage {
	return api.ChatMessage{
		Id: m.ID, Agent: m.Agent, Role: api.ChatRole(m.Role), Author: m.Author, Text: m.Text,
		AssignmentId: optional(m.AssignmentID), CreatedAt: m.CreatedAt,
	}
}

func toChart(c domain.Chart) api.Chart {
	var updated time.Time
	for _, a := range c.Agents() {
		if a.CreatedAt.After(updated) {
			updated = a.CreatedAt
		}
	}
	return api.Chart{Roots: toChartNodes(c.Tree()), AgentCount: c.ActiveCount(), UpdatedAt: updated}
}

func toChartNodes(nodes []domain.Node) []api.ChartNode {
	out := make([]api.ChartNode, 0, len(nodes))
	for _, n := range nodes {
		a := n.Agent
		out = append(out, api.ChartNode{
			Agent: a.Name, DisplayName: a.DisplayName, Kind: api.AgentKind(a.Kind), IsRoot: a.IsRoot,
			State: api.AgentState(a.State), Role: a.Role, Children: toChartNodes(n.Children),
		})
	}
	return out
}

func toCharter(c domain.Charter) api.Charter {
	by := c.UpdatedBy
	if by == "" {
		by = "system:daemon"
	}
	return api.Charter{Text: c.Text, Version: c.Version, UpdatedAt: c.UpdatedAt, UpdatedBy: by}
}

func toGoal(g domain.Goal) api.Goal {
	return api.Goal{
		Id: g.ID, Owner: g.Owner, Title: g.Title, Detail: g.Detail, Status: api.GoalStatus(g.Status),
		ParentId: optional(g.ParentID), CreatedAt: g.CreatedAt, UpdatedAt: g.UpdatedAt,
	}
}

func goalSpec(in api.GoalCreate) domain.GoalSpec {
	return domain.GoalSpec{
		Owner: valueOr(in.Owner, ""), Title: in.Title, Detail: valueOr(in.Detail, ""), ParentID: valueOr(in.ParentId, ""),
	}
}

func goalUpdate(in api.GoalUpdate) domain.GoalUpdate {
	u := domain.GoalUpdate{Title: in.Title, Detail: in.Detail}
	if in.Status != nil {
		s := domain.GoalStatus(*in.Status)
		u.Status = &s
	}
	return u
}

func toBudget(p domain.Pool) api.Budget {
	return api.Budget{
		Currency: p.Currency, TotalMicros: p.TotalMicros, SpentMicros: p.SpentMicros,
		RemainingMicros: p.RemainingMicros(), State: api.BudgetState(p.State()), Wall: api.BudgetWallHard,
		Defence: api.DefenceSlice{
			Enabled: p.Defence.Enabled, ReservedMicros: p.Defence.ReservedMicros,
			SpentMicros: p.Defence.SpentMicros, Wall: api.WallHard,
		},
		PeriodStart: p.PeriodStart, PeriodEnd: p.PeriodEnd, CheckIntervalSeconds: p.CheckIntervalSeconds, AsOf: p.AsOf,
	}
}

func toBudgetNode(n domain.BudgetNode) api.BudgetNode {
	return api.BudgetNode{
		Agent: n.Agent, Manager: optional(n.Manager), AllocatedMicros: n.AllocatedMicros,
		SpentMicros: n.SpentMicros, RemainingMicros: n.RemainingMicros, State: api.BudgetState(n.State),
		Wall: api.BudgetNodeWallSoft,
	}
}

func toBudgetSplit(s domain.Split) api.BudgetSplit {
	p := s.Pool()
	currency := p.Currency
	if !p.Known {
		currency = defaultCurrency
	}
	nodes := make([]api.BudgetNode, 0)
	for _, n := range s.Nodes() {
		nodes = append(nodes, toBudgetNode(n))
	}
	return api.BudgetSplit{Currency: currency, Nodes: nodes, AsOf: p.AsOf}
}

func outputFrame(f app.OutputFrame) api.OutputFrame {
	return api.OutputFrame{Kind: api.OutputFrameKindOutput, Agent: f.Agent, Stream: api.OutputFrameStream(f.Stream), Text: f.Text, Time: f.Time}
}

func stateFrame(f app.StateFrame) api.StateFrame {
	return api.StateFrame{Kind: api.StateFrameKindState, Agent: f.Agent, State: api.AgentState(f.State), Time: f.Time}
}

func heartbeat(t time.Time) api.Heartbeat {
	return api.Heartbeat{Kind: api.HeartbeatKindHeartbeat, Time: t.UTC()}
}
