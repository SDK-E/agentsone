// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/SDK-E/agentsone/internal/contract/common"
	"github.com/SDK-E/agentsone/internal/contract/eventdata"
	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/sched/domain"
)

// clockActor is who a fired schedule acts as.
var clockActor = domain.Actor{Kind: domain.ActorSystem, Name: "clock"}

// Schedules are the schedule use cases.
type Schedules struct {
	Deps
	repo domain.ScheduleRepository
}

// NewSchedules returns the schedule use cases over repo.
func NewSchedules(d Deps, repo domain.ScheduleRepository) *Schedules {
	return &Schedules{Deps: d, repo: repo}
}

// CreateScheduleInput is a new timer.
type CreateScheduleInput struct {
	Agent    string // empty means the caller, which must then be an agent
	Name     string
	Calendar string // systemd OnCalendar, in UTC
	Message  string
	Enabled  *bool // nil means enabled
}

// Create stores a timer for the caller or for an agent below the caller.
func (s *Schedules) Create(ctx context.Context, in CreateScheduleInput) (domain.Schedule, error) {
	actor, err := actorOf(ctx)
	if err != nil {
		return domain.Schedule{}, err
	}
	agent := in.Agent
	if agent == "" {
		if !isAgent(actor) {
			return domain.Schedule{}, fmt.Errorf("%w: name the agent this timer is for", core.ErrInvalid)
		}
		agent = actor.Name
	}
	if err := s.authorize(ctx, actor, agent); err != nil {
		return domain.Schedule{}, err
	}
	enabled := in.Enabled == nil || *in.Enabled
	sc, err := domain.NewSchedule(domain.NewScheduleParams{
		ID: s.IDs.New("sch"), Agent: agent, Name: in.Name, Calendar: in.Calendar, Message: in.Message,
		Enabled: enabled, CreatedBy: actor, Now: s.Clock.Now(),
	})
	if err != nil {
		return domain.Schedule{}, coreErr(err)
	}
	if err := s.repo.Insert(ctx, sc); err != nil {
		return domain.Schedule{}, coreErr(err)
	}
	s.audit(ctx, actor, "schedule.create", "schedule:"+sc.ID, map[string]any{
		"agent": sc.Agent, "name": sc.Name, "calendar": sc.Calendar.String(), "enabled": sc.Enabled,
	})
	return sc, nil
}

// Update changes a timer its owner, or an agent above the owner, may manage.
func (s *Schedules) Update(ctx context.Context, id string, e domain.ScheduleEdit) (domain.Schedule, error) {
	actor, sc, err := s.loadManaged(ctx, id)
	if err != nil {
		return domain.Schedule{}, err
	}
	if err := sc.Edit(e, s.Clock.Now()); err != nil {
		return domain.Schedule{}, coreErr(err)
	}
	if err := s.repo.Update(ctx, sc); err != nil {
		return domain.Schedule{}, coreErr(err)
	}
	s.audit(ctx, actor, "schedule.update", "schedule:"+sc.ID, map[string]any{
		"agent": sc.Agent, "name": sc.Name, "calendar": sc.Calendar.String(), "enabled": sc.Enabled,
	})
	return sc, nil
}

// Delete removes a timer its owner, or an agent above the owner, may manage.
func (s *Schedules) Delete(ctx context.Context, id string) error {
	actor, sc, err := s.loadManaged(ctx, id)
	if err != nil {
		return err
	}
	if err := s.repo.Delete(ctx, id); err != nil {
		return coreErr(err)
	}
	s.audit(ctx, actor, "schedule.delete", "schedule:"+id, map[string]any{"agent": sc.Agent, "name": sc.Name})
	return nil
}

// Get returns one timer.
func (s *Schedules) Get(ctx context.Context, id string) (domain.Schedule, error) {
	if _, err := actorOf(ctx); err != nil {
		return domain.Schedule{}, err
	}
	return s.load(ctx, id)
}

// List returns timers, oldest first.
func (s *Schedules) List(ctx context.Context, f domain.ScheduleFilter) ([]domain.Schedule, error) {
	if _, err := actorOf(ctx); err != nil {
		return nil, err
	}
	list, err := s.repo.List(ctx, f)
	return list, coreErr(err)
}

func (s *Schedules) load(ctx context.Context, id string) (domain.Schedule, error) {
	sc, err := s.repo.Get(ctx, id)
	if errors.Is(err, domain.ErrNotFound) {
		return domain.Schedule{}, fmt.Errorf("schedule %q: %w", id, core.ErrNotFound)
	}
	return sc, coreErr(err)
}

// loadManaged reads a timer and checks the caller may manage it.
func (s *Schedules) loadManaged(ctx context.Context, id string) (domain.Actor, domain.Schedule, error) {
	actor, err := actorOf(ctx)
	if err != nil {
		return domain.Actor{}, domain.Schedule{}, err
	}
	sc, err := s.load(ctx, id)
	if err != nil {
		return domain.Actor{}, domain.Schedule{}, err
	}
	if err := s.authorize(ctx, actor, sc.Agent); err != nil {
		return domain.Actor{}, domain.Schedule{}, err
	}
	return actor, sc, nil
}

// authorize checks the agent exists and actor may manage its timers.
func (s *Schedules) authorize(ctx context.Context, actor domain.Actor, agent string) error {
	if err := s.requireAgent(ctx, agent); err != nil {
		return err
	}
	owner, err := s.standing(ctx, actor, agent)
	if err != nil {
		return err
	}
	return coreErr(domain.CanManageSchedule(actor, owner))
}

// Tick fires every timer that is due and returns how many fired. A timer
// that came due while the daemon was down fires once, not once per missed
// run. A frozen agent is never woken: its timer stays due and fires once
// after the thaw. A timer whose wake failed stays due and is tried again on
// the next tick. The error joins the failures of the timers that did not fire.
func (s *Schedules) Tick(ctx context.Context) (int, error) {
	now := s.Clock.Now()
	due, err := s.repo.Due(ctx, now)
	if err != nil {
		return 0, fmt.Errorf("find due schedules: %w", coreErr(err))
	}
	var (
		fired int
		errs  []error
	)
	for _, sc := range due {
		ok, err := s.fire(ctx, sc, now)
		if err != nil {
			errs = append(errs, fmt.Errorf("schedule %s: %w", sc.ID, err))
		}
		if ok {
			fired++
		}
	}
	return fired, errors.Join(errs...)
}

// timerWake is the payload of a timer wake, as the runner protocol defines it.
type timerWake struct {
	ScheduleID string    `json:"scheduleId"`
	Name       string    `json:"name"`
	Due        time.Time `json:"due"`
	FiredAt    time.Time `json:"firedAt"`
	Note       string    `json:"note,omitempty"`
}

func (s *Schedules) fire(ctx context.Context, sc domain.Schedule, now time.Time) (bool, error) {
	if _, err := s.Ports.Agents.GetAgent(ctx, sc.Agent); errors.Is(err, core.ErrNotFound) {
		s.Log.WarnContext(ctx, "schedule of an agent that is gone", "schedule", sc.ID, "agent", sc.Agent)
		return false, nil
	} else if err != nil {
		return false, fmt.Errorf("read the agent: %w", err)
	}
	frozen, err := s.frozen(ctx, sc.Agent)
	if err != nil || frozen {
		return false, err
	}
	due, err := sc.Fire(now)
	if err != nil {
		return false, coreErr(err)
	}
	payload, err := json.Marshal(timerWake{ScheduleID: sc.ID, Name: sc.Name, Due: due, FiredAt: now, Note: sc.Message})
	if err != nil {
		return false, fmt.Errorf("encode the wake: %w", err)
	}
	// Wake first and save after: a wake that fails leaves the timer due, so
	// the next tick tries again. A save that fails after a wake fires again.
	switch err := s.Ports.Waker.Wake(ctx, sc.Agent, core.WakeTimer, payload); {
	case errors.Is(err, core.ErrFrozen):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("wake %s: %w", sc.Agent, err)
	}
	if err := s.repo.Update(ctx, sc); err != nil {
		return false, coreErr(err)
	}
	s.audit(ctx, clockActor, "schedule.fire", "schedule:"+sc.ID, map[string]any{
		"agent": sc.Agent, "name": sc.Name, "due": due.Format(time.RFC3339),
	})
	s.publishFired(ctx, sc, due)
	return true, nil
}

// publishFired announces the fire on the bus. The agent is already woken, so
// a failure is logged and does not undo the fire.
func (s *Schedules) publishFired(ctx context.Context, sc domain.Schedule, due time.Time) {
	_, err := core.PublishData(ctx, s.Ports.Events, core.Publication{
		Source: "/emitters/clock", Subject: sc.ID, Actor: clockActor.String(), ChartScope: "agent:" + sc.Agent,
		Data: eventdata.ClockScheduleFired{Agent: sc.Agent, ScheduleID: sc.ID, Name: sc.EventName(), Due: common.Timestamp(due)},
	})
	if err != nil {
		s.Log.WarnContext(ctx, "publish schedule event", "schedule", sc.ID, "err", err)
	}
}
