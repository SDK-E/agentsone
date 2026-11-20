// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/SDK-E/agentsone/internal/contract/common"
	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/sched/domain"
	"github.com/SDK-E/agentsone/runners/sdk/protocol"
)

// Tasks are the task use cases. Every change checks who may make it, saves,
// records an audit entry and tells the task stream.
type Tasks struct {
	Deps
	repo domain.TaskRepository
	hub  *hub
}

var _ core.TaskService = (*Tasks)(nil)

// NewTasks returns the task use cases over repo.
func NewTasks(d Deps, repo domain.TaskRepository) *Tasks {
	return &Tasks{Deps: d, repo: repo, hub: newHub(uint64(d.Clock.Now().UnixMicro()))}
}

// CreateInput is a new task, as a caller writes it.
type CreateInput struct {
	Title    string
	Detail   string
	Priority domain.Priority // empty means normal
	Assignee string          // agent name, empty for an unassigned task
	ParentID string
	DueAt    *time.Time
}

// newTask is a task to create, with the facts only the daemon's own parts know.
type newTask struct {
	CreateInput
	creator   domain.Actor
	source    domain.Source
	triggerID string
}

// Create stores a task. Giving it an assignee wakes that agent.
func (s *Tasks) Create(ctx context.Context, in CreateInput) (domain.Task, error) {
	actor, err := actorOf(ctx)
	if err != nil {
		return domain.Task{}, err
	}
	return s.create(ctx, newTask{CreateInput: in, creator: actor, source: sourceOf(actor)})
}

func (s *Tasks) create(ctx context.Context, n newTask) (domain.Task, error) {
	task, err := domain.NewTask(domain.NewTaskParams{
		ID: s.IDs.New("tsk"), Title: n.Title, Detail: n.Detail, Priority: n.Priority,
		Assignee: n.Assignee, Source: n.source, CreatedBy: n.creator,
		ParentID: n.ParentID, TriggerID: n.triggerID, DueAt: n.DueAt, Now: s.Clock.Now(),
	})
	if err != nil {
		return domain.Task{}, coreErr(err)
	}
	if n.Assignee != "" {
		if err := s.checkAssignable(ctx, n.creator, n.Assignee); err != nil {
			return domain.Task{}, err
		}
	}
	if n.ParentID != "" {
		if _, err := s.load(ctx, n.ParentID); err != nil {
			return domain.Task{}, fmt.Errorf("parent task: %w", err)
		}
	}
	if err := s.repo.Insert(ctx, task); err != nil {
		return domain.Task{}, coreErr(err)
	}
	s.audit(ctx, n.creator, "task.create", "task:"+task.ID, map[string]any{
		"title": task.Title, "assignee": task.Assignee, "source": string(task.Source),
	})
	s.hub.publish(task)
	s.wakeAssignee(ctx, n.creator, task)
	return task, nil
}

// Assign gives the task to an agent and wakes it. An agent may assign to
// itself or to an agent below it; a human may assign to any agent.
func (s *Tasks) Assign(ctx context.Context, id, agent string) (domain.Task, error) {
	actor, err := actorOf(ctx)
	if err != nil {
		return domain.Task{}, err
	}
	task, err := s.load(ctx, id)
	if err != nil {
		return domain.Task{}, err
	}
	if err := s.checkAssignable(ctx, actor, agent); err != nil {
		return domain.Task{}, err
	}
	if err := task.Assign(agent, s.Clock.Now()); err != nil {
		return domain.Task{}, coreErr(err)
	}
	if err := s.repo.Update(ctx, task); err != nil {
		return domain.Task{}, coreErr(err)
	}
	s.audit(ctx, actor, "task.assign", "task:"+task.ID, map[string]any{"assignee": agent})
	s.hub.publish(task)
	s.wakeAssignee(ctx, actor, task)
	return task, nil
}

// Complete reports how the task ended: done or failed, with a result. The
// holder or an agent above it may report.
func (s *Tasks) Complete(ctx context.Context, id string, outcome domain.Status, result string) (domain.Task, error) {
	actor, err := actorOf(ctx)
	if err != nil {
		return domain.Task{}, err
	}
	task, err := s.load(ctx, id)
	if err != nil {
		return domain.Task{}, err
	}
	holder, err := s.standing(ctx, actor, task.Assignee)
	if err != nil {
		return domain.Task{}, err
	}
	if err := task.CanComplete(actor, holder); err != nil {
		return domain.Task{}, coreErr(err)
	}
	if err := task.Complete(outcome, result, s.Clock.Now()); err != nil {
		return domain.Task{}, coreErr(err)
	}
	return s.save(ctx, actor, "task.complete", task, map[string]any{"status": string(task.Status)})
}

// Cancel closes the task as cancelled. The creator or an agent above it may.
func (s *Tasks) Cancel(ctx context.Context, id string) (domain.Task, error) {
	actor, err := actorOf(ctx)
	if err != nil {
		return domain.Task{}, err
	}
	task, err := s.load(ctx, id)
	if err != nil {
		return domain.Task{}, err
	}
	creator, err := s.standingToActor(ctx, actor, task.CreatedBy)
	if err != nil {
		return domain.Task{}, err
	}
	if err := task.CanCancel(actor, creator); err != nil {
		return domain.Task{}, coreErr(err)
	}
	if err := task.Cancel(s.Clock.Now()); err != nil {
		return domain.Task{}, coreErr(err)
	}
	return s.save(ctx, actor, "task.cancel", task, nil)
}

// Edit changes a task's title, detail or priority. The creator, the holder
// or an agent above either may.
func (s *Tasks) Edit(ctx context.Context, id string, e domain.TaskEdit) (domain.Task, error) {
	actor, err := actorOf(ctx)
	if err != nil {
		return domain.Task{}, err
	}
	task, err := s.load(ctx, id)
	if err != nil {
		return domain.Task{}, err
	}
	creator, err := s.standingToActor(ctx, actor, task.CreatedBy)
	if err != nil {
		return domain.Task{}, err
	}
	holder, err := s.standing(ctx, actor, task.Assignee)
	if err != nil {
		return domain.Task{}, err
	}
	if err := task.CanEdit(actor, creator, holder); err != nil {
		return domain.Task{}, coreErr(err)
	}
	if err := task.Edit(e, s.Clock.Now()); err != nil {
		return domain.Task{}, coreErr(err)
	}
	return s.save(ctx, actor, "task.edit", task, nil)
}

// Get returns one task.
func (s *Tasks) Get(ctx context.Context, id string) (domain.Task, error) {
	if _, err := actorOf(ctx); err != nil {
		return domain.Task{}, err
	}
	return s.load(ctx, id)
}

// List returns tasks, newest first.
func (s *Tasks) List(ctx context.Context, f domain.TaskFilter) ([]domain.Task, error) {
	if _, err := actorOf(ctx); err != nil {
		return nil, err
	}
	tasks, err := s.repo.List(ctx, f)
	return tasks, coreErr(err)
}

// Subscribe streams every task change as a frame until ctx ends. A lastID
// from an earlier stream replays the frames after it first.
func (s *Tasks) Subscribe(ctx context.Context, lastID string) <-chan Frame {
	return s.hub.subscribe(ctx, lastID)
}

// load reads a task and says which one is missing.
func (s *Tasks) load(ctx context.Context, id string) (domain.Task, error) {
	task, err := s.repo.Get(ctx, id)
	if errors.Is(err, domain.ErrNotFound) {
		return domain.Task{}, fmt.Errorf("task %q: %w", id, core.ErrNotFound)
	}
	return task, coreErr(err)
}

// save stores a changed task, then records and announces the change.
func (s *Tasks) save(ctx context.Context, actor domain.Actor, action string, task domain.Task, detail map[string]any) (domain.Task, error) {
	if err := s.repo.Update(ctx, task); err != nil {
		return domain.Task{}, coreErr(err)
	}
	s.audit(ctx, actor, action, "task:"+task.ID, detail)
	s.hub.publish(task)
	return task, nil
}

// checkAssignable says whether actor may give a task to the agent now: it
// exists, the chart allows it, and it is not frozen.
func (s *Tasks) checkAssignable(ctx context.Context, actor domain.Actor, agent string) error {
	if err := s.requireAgent(ctx, agent); err != nil {
		return err
	}
	to, err := s.standing(ctx, actor, agent)
	if err != nil {
		return err
	}
	if err := domain.CanAssign(actor, to); err != nil {
		return coreErr(err)
	}
	frozen, err := s.frozen(ctx, agent)
	if err != nil {
		return err
	}
	if frozen {
		return fmt.Errorf("agent %q: %w", agent, core.ErrFrozen)
	}
	return nil
}

// taskWake is task as the runner contract's wake payload for reason "task".
func taskWake(task domain.Task) protocol.Task {
	out := protocol.Task{
		ID:        common.TaskID(task.ID),
		Title:     common.OneLine(task.Title),
		Body:      common.Markdown(task.Detail),
		From:      common.ActorRef(task.CreatedBy.String()),
		CreatedAt: common.Timestamp(task.CreatedAt),
	}
	if task.DueAt != nil {
		d := common.Timestamp(*task.DueAt)
		out.Due = &d
	}
	return out
}

// wakeAssignee wakes the task's assignee. The task is already saved, so a
// failed wake is recorded and does not undo it.
func (s *Tasks) wakeAssignee(ctx context.Context, actor domain.Actor, task domain.Task) {
	if task.Assignee == "" {
		return
	}
	payload, err := json.Marshal(taskWake(task))
	if err != nil {
		s.Log.ErrorContext(ctx, "encode task wake", "task", task.ID, "err", err)
		return
	}
	err = s.Ports.Waker.Wake(ctx, task.Assignee, core.WakeTask, payload)
	switch {
	case err == nil:
	case errors.Is(err, core.ErrFrozen):
		s.audit(ctx, actor, "task.wake.skipped", "task:"+task.ID, map[string]any{"agent": task.Assignee, "reason": "frozen"})
	default:
		s.Log.ErrorContext(ctx, "wake the assignee", "task", task.ID, "agent", task.Assignee, "err", err)
		s.auditFailure(ctx, actor, "task.wake.failed", "task:"+task.ID, map[string]any{"agent": task.Assignee}, err)
	}
}

// sourceOf is how a task made by actor came to exist.
func sourceOf(actor domain.Actor) domain.Source {
	switch actor.Kind {
	case domain.ActorAgent:
		return domain.SourceAgent
	case domain.ActorHuman, domain.ActorHost:
		return domain.SourceHuman
	case domain.ActorSystem:
		switch actor.Name {
		case "router":
			return domain.SourceRouter
		case "clock":
			return domain.SourceSchedule
		}
	}
	return domain.SourceTrigger
}
