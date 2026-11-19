// SPDX-License-Identifier: MIT

package httpadapter

import (
	"context"

	"github.com/SDK-E/agentsone/internal/contract/api"
	"github.com/SDK-E/agentsone/internal/contract/api/schedulesapi"
	"github.com/SDK-E/agentsone/internal/sched/app"
	"github.com/SDK-E/agentsone/internal/sched/domain"
)

// Schedules serves the schedules tag.
type Schedules struct {
	schedulesapi.Unimplemented
	svc *app.Schedules
}

var _ schedulesapi.StrictServerInterface = (*Schedules)(nil)

// NewSchedules returns the schedules handler.
func NewSchedules(svc *app.Schedules) *Schedules { return &Schedules{svc: svc} }

// ListSchedules returns one page of schedules, oldest first.
func (h *Schedules) ListSchedules(ctx context.Context, req schedulesapi.ListSchedulesRequestObject) (schedulesapi.ListSchedulesResponseObject, error) {
	p := req.Params
	limit, err := pageSize(p.Limit)
	if err != nil {
		return nil, err
	}
	after, err := decodeCursor(p.Cursor)
	if err != nil {
		return nil, err
	}
	list, err := h.svc.List(ctx, domain.ScheduleFilter{
		Agent: valueOr(p.Agent, ""), AfterID: after, Limit: limit + 1,
	})
	if err != nil {
		return nil, err
	}
	out := api.ScheduleList{Items: []api.Schedule{}}
	if len(list) > limit {
		list = list[:limit]
		out.NextCursor = encodeCursor(list[limit-1].ID)
	}
	for _, s := range list {
		out.Items = append(out.Items, toSchedule(s))
	}
	return schedulesapi.ListSchedules200JSONResponse(out), nil
}

// CreateSchedule stores a timer.
func (h *Schedules) CreateSchedule(ctx context.Context, req schedulesapi.CreateScheduleRequestObject) (schedulesapi.CreateScheduleResponseObject, error) {
	b, err := body(req.Body)
	if err != nil {
		return nil, err
	}
	s, err := h.svc.Create(ctx, app.CreateScheduleInput{
		Agent: valueOr(b.Agent, ""), Name: b.Name, Calendar: b.Calendar,
		Message: b.Message, Enabled: b.Enabled,
	})
	if err != nil {
		return nil, err
	}
	return schedulesapi.CreateSchedule201JSONResponse(toSchedule(s)), nil
}

// GetSchedule returns one timer.
func (h *Schedules) GetSchedule(ctx context.Context, req schedulesapi.GetScheduleRequestObject) (schedulesapi.GetScheduleResponseObject, error) {
	s, err := h.svc.Get(ctx, req.ScheduleId)
	if err != nil {
		return nil, err
	}
	return schedulesapi.GetSchedule200JSONResponse(toSchedule(s)), nil
}

// UpdateSchedule changes a timer.
func (h *Schedules) UpdateSchedule(ctx context.Context, req schedulesapi.UpdateScheduleRequestObject) (schedulesapi.UpdateScheduleResponseObject, error) {
	b, err := body(req.Body)
	if err != nil {
		return nil, err
	}
	s, err := h.svc.Update(ctx, req.ScheduleId, domain.ScheduleEdit{
		Name: b.Name, Calendar: b.Calendar, Message: b.Message, Enabled: b.Enabled,
	})
	if err != nil {
		return nil, err
	}
	return schedulesapi.UpdateSchedule200JSONResponse(toSchedule(s)), nil
}

// DeleteSchedule removes a timer.
func (h *Schedules) DeleteSchedule(ctx context.Context, req schedulesapi.DeleteScheduleRequestObject) (schedulesapi.DeleteScheduleResponseObject, error) {
	if err := h.svc.Delete(ctx, req.ScheduleId); err != nil {
		return nil, err
	}
	return schedulesapi.DeleteSchedule204Response{}, nil
}
