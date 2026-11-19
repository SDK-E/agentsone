// SPDX-License-Identifier: MIT

package httpadapter

import (
	"context"

	"github.com/SDK-E/agentsone/internal/agents/domain"
	"github.com/SDK-E/agentsone/internal/contract/api"
	"github.com/SDK-E/agentsone/internal/contract/api/budgetapi"
	"github.com/SDK-E/agentsone/internal/contract/api/chartapi"
	"github.com/SDK-E/agentsone/internal/contract/api/charterapi"
)

// GetChart returns the org chart as a tree.
func (h *Handler) GetChart(ctx context.Context, _ chartapi.GetChartRequestObject) (chartapi.GetChartResponseObject, error) {
	c, err := h.s.Chart(ctx)
	if err != nil {
		return nil, err
	}
	return chartapi.GetChart200JSONResponse(toChart(c)), nil
}

// MoveAgent moves an agent and its branch, and returns the new chart.
func (h *Handler) MoveAgent(ctx context.Context, req chartapi.MoveAgentRequestObject) (chartapi.MoveAgentResponseObject, error) {
	if err := h.s.Move(ctx, req.Body.Agent, req.Body.NewManager); err != nil {
		return nil, err
	}
	c, err := h.s.Chart(ctx)
	if err != nil {
		return nil, err
	}
	return chartapi.MoveAgent200JSONResponse(toChart(c)), nil
}

// GetCharter returns the charter; version 0 means none yet.
func (h *Handler) GetCharter(ctx context.Context, _ charterapi.GetCharterRequestObject) (charterapi.GetCharterResponseObject, error) {
	c, err := h.s.Charter(ctx)
	if err != nil {
		return nil, err
	}
	return charterapi.GetCharter200JSONResponse(toCharter(c)), nil
}

// UpdateCharter rewrites the charter.
func (h *Handler) UpdateCharter(ctx context.Context, req charterapi.UpdateCharterRequestObject) (charterapi.UpdateCharterResponseObject, error) {
	c, err := h.s.RewriteCharter(ctx, req.Body.ExpectedVersion, req.Body.Text)
	if err != nil {
		return nil, err
	}
	return charterapi.UpdateCharter200JSONResponse(toCharter(c)), nil
}

// ListGoals returns one page of goals sorted by id. The cursor is the last
// id of the previous page.
func (h *Handler) ListGoals(ctx context.Context, req charterapi.ListGoalsRequestObject) (charterapi.ListGoalsResponseObject, error) {
	p := req.Params
	limit := limitOf(p.Limit)
	f := domain.GoalFilter{Owner: valueOr(p.Owner, ""), AfterID: valueOr(p.Cursor, ""), Limit: limit + 1}
	if p.Status != nil {
		f.Status = domain.GoalStatus(*p.Status)
	}
	goals, err := h.s.Goals(ctx, f)
	if err != nil {
		return nil, err
	}
	out := api.GoalList{Items: []api.Goal{}}
	for i, g := range goals {
		if i == limit {
			last := out.Items[limit-1].Id
			out.NextCursor = &last
			break
		}
		out.Items = append(out.Items, toGoal(g))
	}
	return charterapi.ListGoals200JSONResponse(out), nil
}

// CreateGoal sets a goal for the caller or an agent below it.
func (h *Handler) CreateGoal(ctx context.Context, req charterapi.CreateGoalRequestObject) (charterapi.CreateGoalResponseObject, error) {
	g, err := h.s.SetGoal(ctx, goalSpec(*req.Body))
	if err != nil {
		return nil, err
	}
	return charterapi.CreateGoal201JSONResponse(toGoal(g)), nil
}

// DeleteGoal deletes a goal.
func (h *Handler) DeleteGoal(ctx context.Context, req charterapi.DeleteGoalRequestObject) (charterapi.DeleteGoalResponseObject, error) {
	if err := h.s.DeleteGoal(ctx, req.GoalId); err != nil {
		return nil, err
	}
	return charterapi.DeleteGoal204Response{}, nil
}

// GetGoal returns one goal.
func (h *Handler) GetGoal(ctx context.Context, req charterapi.GetGoalRequestObject) (charterapi.GetGoalResponseObject, error) {
	g, err := h.s.Goal(ctx, req.GoalId)
	if err != nil {
		return nil, err
	}
	return charterapi.GetGoal200JSONResponse(toGoal(g)), nil
}

// UpdateGoal edits a goal.
func (h *Handler) UpdateGoal(ctx context.Context, req charterapi.UpdateGoalRequestObject) (charterapi.UpdateGoalResponseObject, error) {
	g, err := h.s.UpdateGoal(ctx, req.GoalId, goalUpdate(*req.Body))
	if err != nil {
		return nil, err
	}
	return charterapi.UpdateGoal200JSONResponse(toGoal(g)), nil
}

// GetBudget returns the total as the host last pushed it (the hard wall).
func (h *Handler) GetBudget(ctx context.Context, _ budgetapi.GetBudgetRequestObject) (budgetapi.GetBudgetResponseObject, error) {
	p, err := h.s.Budget().Pool(ctx)
	if err != nil {
		return nil, err
	}
	return budgetapi.GetBudget200JSONResponse(toBudget(p)), nil
}

// SetBudgetAllocation sets the soft allocation of an agent below the caller.
func (h *Handler) SetBudgetAllocation(ctx context.Context, req budgetapi.SetBudgetAllocationRequestObject) (budgetapi.SetBudgetAllocationResponseObject, error) {
	n, err := h.s.Budget().Allocate(ctx, req.AgentName, req.Body.AllocatedMicros)
	if err != nil {
		return nil, err
	}
	return budgetapi.SetBudgetAllocation200JSONResponse(toBudgetNode(n)), nil
}

// GetBudgetSplit returns every agent's soft share, parents first.
func (h *Handler) GetBudgetSplit(ctx context.Context, _ budgetapi.GetBudgetSplitRequestObject) (budgetapi.GetBudgetSplitResponseObject, error) {
	s, err := h.s.Budget().Split(ctx)
	if err != nil {
		return nil, err
	}
	return budgetapi.GetBudgetSplit200JSONResponse(toBudgetSplit(s)), nil
}
