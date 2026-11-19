// SPDX-License-Identifier: MIT

package httpadapter

import (
	"context"
	"errors"
	"fmt"

	"github.com/SDK-E/agentsone/internal/agents/app"
	"github.com/SDK-E/agentsone/internal/agents/domain"
	"github.com/SDK-E/agentsone/internal/contract/api"
	"github.com/SDK-E/agentsone/internal/contract/api/agentsapi"
	"github.com/SDK-E/agentsone/internal/core"
)

// view holds what every agent DTO needs besides the agent: the chart (for
// its team) and the live list (for its one-line).
type view struct {
	chart domain.Chart
	lines map[string]core.LiveLine
}

func (h *Handler) view(ctx context.Context) (view, error) {
	chart, err := h.s.Chart(ctx)
	if err != nil {
		return view{}, err
	}
	lines, err := h.live.Lines(ctx)
	if err != nil && !errors.Is(err, core.ErrUnavailable) {
		return view{}, fmt.Errorf("read live list: %w", err)
	}
	v := view{chart: chart, lines: map[string]core.LiveLine{}}
	for _, l := range lines {
		v.lines[l.Agent] = l
	}
	return v, nil
}

func (v view) agent(a domain.Agent) api.Agent {
	var line *core.LiveLine
	if l, ok := v.lines[a.Name]; ok {
		line = &l
	}
	return toAgent(a, v.chart.Team(a.Name), line)
}

func (h *Handler) agentDTO(ctx context.Context, a domain.Agent) (api.Agent, error) {
	v, err := h.view(ctx)
	if err != nil {
		return api.Agent{}, err
	}
	return v.agent(a), nil
}

// ListAgents returns one page of agents sorted by name. The cursor is the
// last name of the previous page.
func (h *Handler) ListAgents(ctx context.Context, req agentsapi.ListAgentsRequestObject) (agentsapi.ListAgentsResponseObject, error) {
	p := req.Params
	f := app.ListFilter{Manager: valueOr(p.Manager, ""), Team: valueOr(p.Team, ""), IsRoot: p.IsRoot}
	if p.Kind != nil {
		f.Kind = domain.Kind(*p.Kind)
	}
	if p.State != nil {
		f.State = domain.State(*p.State)
	}
	list, err := h.s.List(ctx, f)
	if err != nil {
		return nil, err
	}
	v, err := h.view(ctx)
	if err != nil {
		return nil, err
	}
	after, limit := valueOr(p.Cursor, ""), limitOf(p.Limit)
	out := api.AgentList{Items: []api.Agent{}}
	for _, a := range list {
		if a.Name <= after {
			continue
		}
		if len(out.Items) == limit {
			last := out.Items[len(out.Items)-1].Name
			out.NextCursor = &last
			break
		}
		out.Items = append(out.Items, v.agent(a))
	}
	return agentsapi.ListAgents200JSONResponse(out), nil
}

// HireAgent hires an agent below the caller.
func (h *Handler) HireAgent(ctx context.Context, req agentsapi.HireAgentRequestObject) (agentsapi.HireAgentResponseObject, error) {
	a, err := h.s.Hire(ctx, hireSpec(*req.Body))
	if err != nil {
		return nil, err
	}
	dto, err := h.agentDTO(ctx, a)
	if err != nil {
		return nil, err
	}
	return agentsapi.HireAgent201JSONResponse(dto), nil
}

// RetireAgent retires an agent below the caller.
func (h *Handler) RetireAgent(ctx context.Context, req agentsapi.RetireAgentRequestObject) (agentsapi.RetireAgentResponseObject, error) {
	if err := h.s.Retire(ctx, req.AgentName); err != nil {
		return nil, err
	}
	return agentsapi.RetireAgent204Response{}, nil
}

// GetAgent returns one agent.
func (h *Handler) GetAgent(ctx context.Context, req agentsapi.GetAgentRequestObject) (agentsapi.GetAgentResponseObject, error) {
	a, err := h.s.Get(ctx, req.AgentName)
	if err != nil {
		return nil, err
	}
	dto, err := h.agentDTO(ctx, a)
	if err != nil {
		return nil, err
	}
	return agentsapi.GetAgent200JSONResponse(dto), nil
}

// UpdateAgent edits an agent below the caller. Limits the request leaves
// out keep their current values.
func (h *Handler) UpdateAgent(ctx context.Context, req agentsapi.UpdateAgentRequestObject) (agentsapi.UpdateAgentResponseObject, error) {
	current, err := h.s.Get(ctx, req.AgentName)
	if err != nil {
		return nil, err
	}
	a, err := h.s.Update(ctx, req.AgentName, agentUpdate(*req.Body, current))
	if err != nil {
		return nil, err
	}
	dto, err := h.agentDTO(ctx, a)
	if err != nil {
		return nil, err
	}
	return agentsapi.UpdateAgent200JSONResponse(dto), nil
}

// ListChatMessages returns one page of an agent's chat, newest first. The
// cursor is the id of the last message of the previous page.
func (h *Handler) ListChatMessages(ctx context.Context, req agentsapi.ListChatMessagesRequestObject) (agentsapi.ListChatMessagesResponseObject, error) {
	limit := limitOf(req.Params.Limit)
	msgs, err := h.s.Chat(ctx, req.AgentName, valueOr(req.Params.Cursor, ""), limit+1)
	if err != nil {
		return nil, err
	}
	out := api.ChatMessageList{Items: []api.ChatMessage{}}
	for i, m := range msgs {
		if i == limit {
			last := out.Items[limit-1].Id
			out.NextCursor = &last
			break
		}
		out.Items = append(out.Items, toChatMessage(m))
	}
	return agentsapi.ListChatMessages200JSONResponse(out), nil
}

// SendChatMessage posts into an agent's chat and wakes it.
func (h *Handler) SendChatMessage(ctx context.Context, req agentsapi.SendChatMessageRequestObject) (agentsapi.SendChatMessageResponseObject, error) {
	m, err := h.s.SendChat(ctx, req.AgentName, req.Body.Text)
	if err != nil {
		return nil, err
	}
	return agentsapi.SendChatMessage201JSONResponse(toChatMessage(m)), nil
}

// GetAgentOneLine returns the agent's live list entry.
func (h *Handler) GetAgentOneLine(ctx context.Context, req agentsapi.GetAgentOneLineRequestObject) (agentsapi.GetAgentOneLineResponseObject, error) {
	l, err := h.s.OneLine(ctx, req.AgentName)
	if err != nil {
		return nil, err
	}
	return agentsapi.GetAgentOneLine200JSONResponse(toOneLine(l)), nil
}

// SetAgentOneLine sets the caller's own one-line.
func (h *Handler) SetAgentOneLine(ctx context.Context, req agentsapi.SetAgentOneLineRequestObject) (agentsapi.SetAgentOneLineResponseObject, error) {
	l, err := h.s.SetOneLine(ctx, req.AgentName, req.Body.Text)
	if err != nil {
		return nil, err
	}
	return agentsapi.SetAgentOneLine200JSONResponse(toOneLine(l)), nil
}

// SleepAgent puts the caller, or an agent below it, to sleep.
func (h *Handler) SleepAgent(ctx context.Context, req agentsapi.SleepAgentRequestObject) (agentsapi.SleepAgentResponseObject, error) {
	compact := true
	if req.Body != nil {
		compact = valueOr(req.Body.Compact, true)
	}
	a, err := h.s.Sleep(ctx, req.AgentName, compact)
	if err != nil {
		return nil, err
	}
	dto, err := h.agentDTO(ctx, a)
	if err != nil {
		return nil, err
	}
	return agentsapi.SleepAgent202JSONResponse(dto), nil
}

// WakeAgent starts a turn of an agent.
func (h *Handler) WakeAgent(ctx context.Context, req agentsapi.WakeAgentRequestObject) (agentsapi.WakeAgentResponseObject, error) {
	a, err := h.s.WakeAgent(ctx, req.AgentName, app.WakeRequest{Reason: req.Body.Reason, TaskID: valueOr(req.Body.TaskId, "")})
	if err != nil {
		return nil, err
	}
	dto, err := h.agentDTO(ctx, a)
	if err != nil {
		return nil, err
	}
	return agentsapi.WakeAgent202JSONResponse(dto), nil
}
