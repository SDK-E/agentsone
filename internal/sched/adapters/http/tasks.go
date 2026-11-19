// SPDX-License-Identifier: MIT

package httpadapter

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/SDK-E/agentsone/internal/contract/api"
	"github.com/SDK-E/agentsone/internal/contract/api/tasksapi"
	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/sched/app"
	"github.com/SDK-E/agentsone/internal/sched/domain"
)

// DefaultHeartbeat is how often an idle task stream sends a heartbeat.
const DefaultHeartbeat = 15 * time.Second

// Tasks serves the tasks tag.
type Tasks struct {
	tasksapi.Unimplemented
	svc       *app.Tasks
	clock     core.Clock
	heartbeat time.Duration
}

var _ tasksapi.StrictServerInterface = (*Tasks)(nil)

// TasksOption changes how the tasks handler behaves.
type TasksOption func(*Tasks)

// WithHeartbeat sets the idle heartbeat of the task stream. Zero turns it off,
// which tests use to stay free of timers.
func WithHeartbeat(d time.Duration) TasksOption { return func(h *Tasks) { h.heartbeat = d } }

// NewTasks returns the tasks handler. The clock stamps heartbeats.
func NewTasks(svc *app.Tasks, clock core.Clock, opts ...TasksOption) *Tasks {
	h := &Tasks{svc: svc, clock: clock, heartbeat: DefaultHeartbeat}
	for _, o := range opts {
		o(h)
	}
	return h
}

// ListTasks returns one page of tasks, newest first.
func (h *Tasks) ListTasks(ctx context.Context, req tasksapi.ListTasksRequestObject) (tasksapi.ListTasksResponseObject, error) {
	p := req.Params
	limit, err := pageSize(p.Limit)
	if err != nil {
		return nil, err
	}
	before, err := decodeCursor(p.Cursor)
	if err != nil {
		return nil, err
	}
	f := domain.TaskFilter{BeforeID: before, Limit: limit + 1}
	if p.Status != nil {
		f.Status = domain.Status(*p.Status)
	}
	if p.Source != nil {
		f.Source = domain.Source(*p.Source)
	}
	f.Assignee = valueOr(p.Assignee, "")
	tasks, err := h.svc.List(ctx, f)
	if err != nil {
		return nil, err
	}
	out := api.TaskList{Items: []api.Task{}}
	if len(tasks) > limit {
		tasks = tasks[:limit]
		out.NextCursor = encodeCursor(tasks[limit-1].ID)
	}
	for _, t := range tasks {
		out.Items = append(out.Items, toTask(t))
	}
	return tasksapi.ListTasks200JSONResponse(out), nil
}

// CreateTask stores a task for the caller.
func (h *Tasks) CreateTask(ctx context.Context, req tasksapi.CreateTaskRequestObject) (tasksapi.CreateTaskResponseObject, error) {
	b, err := body(req.Body)
	if err != nil {
		return nil, err
	}
	in := app.CreateInput{
		Title: b.Title, Detail: valueOr(b.Detail, ""), Assignee: valueOr(b.Assignee, ""),
		ParentID: valueOr(b.ParentId, ""), DueAt: timeOrNil(b.DueAt),
	}
	if b.Priority != nil {
		in.Priority = domain.Priority(*b.Priority)
	}
	t, err := h.svc.Create(ctx, in)
	if errors.Is(err, core.ErrForbidden) {
		// The contract gives createTask no 403, so a refused assignee is a
		// bad request. assignTask declares 403 and keeps it.
		reason := err.Error() // text only, so the error stays a plain ErrInvalid
		return nil, fmt.Errorf("%w: %s", core.ErrInvalid, reason)
	}
	if err != nil {
		return nil, err
	}
	return tasksapi.CreateTask201JSONResponse(toTask(t)), nil
}

// GetTask returns one task.
func (h *Tasks) GetTask(ctx context.Context, req tasksapi.GetTaskRequestObject) (tasksapi.GetTaskResponseObject, error) {
	t, err := h.svc.Get(ctx, req.TaskId)
	if err != nil {
		return nil, err
	}
	return tasksapi.GetTask200JSONResponse(toTask(t)), nil
}

// UpdateTask edits the wording or priority of a task.
func (h *Tasks) UpdateTask(ctx context.Context, req tasksapi.UpdateTaskRequestObject) (tasksapi.UpdateTaskResponseObject, error) {
	b, err := body(req.Body)
	if err != nil {
		return nil, err
	}
	e := domain.TaskEdit{Title: b.Title, Detail: b.Detail}
	if b.Priority != nil {
		p := domain.Priority(*b.Priority)
		e.Priority = &p
	}
	t, err := h.svc.Edit(ctx, req.TaskId, e)
	if err != nil {
		return nil, err
	}
	return tasksapi.UpdateTask200JSONResponse(toTask(t)), nil
}

// AssignTask gives a task to an agent and wakes it.
func (h *Tasks) AssignTask(ctx context.Context, req tasksapi.AssignTaskRequestObject) (tasksapi.AssignTaskResponseObject, error) {
	b, err := body(req.Body)
	if err != nil {
		return nil, err
	}
	t, err := h.svc.Assign(ctx, req.TaskId, b.Assignee)
	if err != nil {
		return nil, err
	}
	return tasksapi.AssignTask200JSONResponse(toTask(t)), nil
}

// CompleteTask records the outcome of a task.
func (h *Tasks) CompleteTask(ctx context.Context, req tasksapi.CompleteTaskRequestObject) (tasksapi.CompleteTaskResponseObject, error) {
	b, err := body(req.Body)
	if err != nil {
		return nil, err
	}
	t, err := h.svc.Complete(ctx, req.TaskId, domain.Status(b.Outcome), b.Result)
	if err != nil {
		return nil, err
	}
	return tasksapi.CompleteTask200JSONResponse(toTask(t)), nil
}

// CancelTask closes a task as cancelled.
func (h *Tasks) CancelTask(ctx context.Context, req tasksapi.CancelTaskRequestObject) (tasksapi.CancelTaskResponseObject, error) {
	t, err := h.svc.Cancel(ctx, req.TaskId)
	if err != nil {
		return nil, err
	}
	return tasksapi.CancelTask200JSONResponse(toTask(t)), nil
}

// StreamTasks sends every task change as a Server-Sent Event. A client that
// reconnects with Last-Event-ID first gets the frames it missed.
func (h *Tasks) StreamTasks(ctx context.Context, req tasksapi.StreamTasksRequestObject) (tasksapi.StreamTasksResponseObject, error) {
	frames := h.svc.Subscribe(ctx, valueOr(req.Params.LastEventID, ""))
	return tasksapi.StreamTasks200TexteventStreamResponse{
		Body: newSSEReader(ctx, frames, h.clock, h.heartbeat),
	}, nil
}

// body is the decoded request body, which the generated code leaves nil when
// the client sent none.
func body[T any](p *T) (T, error) {
	if p == nil {
		var zero T
		return zero, fmt.Errorf("%w: the request needs a body", core.ErrInvalid)
	}
	return *p, nil
}
