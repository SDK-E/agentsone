// SPDX-License-Identifier: MIT

package httpadapter

import (
	"context"

	"github.com/SDK-E/agentsone/internal/contract/api"
	"github.com/SDK-E/agentsone/internal/contract/api/runnersapi"
	"github.com/SDK-E/agentsone/internal/plugins/domain"
)

// RunnerService is what the runners group needs from the app layer. List
// returns runners sorted by name.
type RunnerService interface {
	List(ctx context.Context) ([]domain.RunnerEntry, error)
	Get(ctx context.Context, name string) (domain.RunnerEntry, error)
}

// RunnersHandler serves the runners group.
type RunnersHandler struct{ svc RunnerService }

var _ runnersapi.StrictServerInterface = (*RunnersHandler)(nil)

// NewRunnersHandler returns the runners handler over the service.
func NewRunnersHandler(svc RunnerService) *RunnersHandler { return &RunnersHandler{svc: svc} }

// ListRunners returns one page of registered runners, built-ins and plugin
// runners together.
func (h *RunnersHandler) ListRunners(ctx context.Context, req runnersapi.ListRunnersRequestObject) (runnersapi.ListRunnersResponseObject, error) {
	all, err := h.svc.List(ctx)
	if err != nil {
		return nil, coreErr(err)
	}
	cursor, limit := paging(req.Params.Cursor, req.Params.Limit)
	items, next := page(all, func(r domain.RunnerEntry) string { return r.Name }, cursor, limit)
	out := runnersapi.ListRunners200JSONResponse{Items: make([]api.Runner, 0, len(items)), NextCursor: nextCursor(next)}
	for _, r := range items {
		out.Items = append(out.Items, toRunner(r))
	}
	return out, nil
}

// GetRunner returns one runner by name.
func (h *RunnersHandler) GetRunner(ctx context.Context, req runnersapi.GetRunnerRequestObject) (runnersapi.GetRunnerResponseObject, error) {
	r, err := h.svc.Get(ctx, req.RunnerName)
	if err != nil {
		return nil, coreErr(err)
	}
	return runnersapi.GetRunner200JSONResponse(toRunner(r)), nil
}

func toRunner(r domain.RunnerEntry) api.Runner {
	dto := api.Runner{
		Name: r.Name, DisplayName: r.DisplayName, Version: r.Version, ProtocolVersion: r.ProtocolVersion,
		Builtin: r.Builtin, Status: api.RunnerStatus(r.Status), Description: r.Description,
	}
	if r.PluginID != "" {
		dto.PluginId = &r.PluginID
	}
	if r.Model != "" {
		dto.Model = &r.Model
	}
	return dto
}
