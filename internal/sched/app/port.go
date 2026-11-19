// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/sched/domain"
)

// CreateTask is the core.TaskService port. A caller that is not an agent or a
// human may name the creator, for example "system:triggers", and a caller
// that is gone from the context entirely is trusted only to name a creator
// that is not an agent. An agent or a human is always the creator.
func (s *Tasks) CreateTask(ctx context.Context, t core.NewTask) (core.Task, error) {
	priority, err := core.TaskPriority(t.Priority)
	if err != nil {
		return core.Task{}, err
	}
	creator, err := portCreator(ctx, t.CreatedBy)
	if err != nil {
		return core.Task{}, err
	}
	task, err := s.create(ctx, newTask{
		CreateInput: CreateInput{Title: t.Title, Detail: t.Body, Priority: domain.Priority(priority), Assignee: t.Assignee},
		creator:     creator, source: sourceOf(creator), triggerID: t.CauseEventID,
	})
	if err != nil {
		return core.Task{}, err
	}
	return toCore(task), nil
}

// portCreator works out who is creating a task through the port.
func portCreator(ctx context.Context, named string) (domain.Actor, error) {
	caller, err := actorOf(ctx)
	if err != nil && !errors.Is(err, core.ErrUnauthorized) {
		return domain.Actor{}, err
	}
	if err == nil && (caller.Kind == domain.ActorAgent || caller.Kind == domain.ActorHuman || named == "") {
		return caller, nil
	}
	if named == "" {
		return domain.Actor{}, err
	}
	creator, perr := domain.ParseActor(named)
	if perr != nil {
		return domain.Actor{}, coreErr(perr)
	}
	if isAgent(creator) {
		return domain.Actor{}, fmt.Errorf("%w: a task for %s must come from that agent", core.ErrUnauthorized, creator)
	}
	return creator, nil
}

// GetTask is the core.TaskService port.
func (s *Tasks) GetTask(ctx context.Context, id string) (core.Task, error) {
	task, err := s.Get(ctx, id)
	if err != nil {
		return core.Task{}, err
	}
	return toCore(task), nil
}

// CompleteTask is the core.TaskService port. The outcomes done and failed
// report a result; cancelled cancels the task.
func (s *Tasks) CompleteTask(ctx context.Context, id string, outcome core.TaskOutcome) (core.Task, error) {
	if err := outcome.Validate(); err != nil {
		return core.Task{}, err
	}
	var (
		task domain.Task
		err  error
	)
	if outcome.Status == core.TaskCancelled {
		task, err = s.Cancel(ctx, id)
	} else {
		task, err = s.Complete(ctx, id, domain.Status(outcome.Status), outcome.Result)
	}
	if err != nil {
		return core.Task{}, err
	}
	return toCore(task), nil
}

func toCore(t domain.Task) core.Task {
	return core.Task{
		ID: t.ID, Title: t.Title, Assignee: t.Assignee, Status: string(t.Status),
		Priority: string(t.Priority), CreatedBy: t.CreatedBy.String(), Result: t.Result, Created: t.CreatedAt,
	}
}
