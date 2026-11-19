// SPDX-License-Identifier: MIT

package app_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/sched/adapters/memory"
	"github.com/SDK-E/agentsone/internal/sched/app"
	"github.com/SDK-E/agentsone/internal/sched/domain"
)

var errBoom = errors.New("boom")

// brokenTasks is a task repository that fails the calls named in its fields.
type brokenTasks struct {
	domain.TaskRepository
	get, insert, update, list error
}

func (r *brokenTasks) Get(ctx context.Context, id string) (domain.Task, error) {
	if r.get != nil {
		return domain.Task{}, r.get
	}
	return r.TaskRepository.Get(ctx, id)
}

func (r *brokenTasks) Insert(ctx context.Context, t domain.Task) error {
	if r.insert != nil {
		return r.insert
	}
	return r.TaskRepository.Insert(ctx, t)
}

func (r *brokenTasks) Update(ctx context.Context, t domain.Task) error {
	if r.update != nil {
		return r.update
	}
	return r.TaskRepository.Update(ctx, t)
}

func (r *brokenTasks) List(ctx context.Context, f domain.TaskFilter) ([]domain.Task, error) {
	if r.list != nil {
		return nil, r.list
	}
	return r.TaskRepository.List(ctx, f)
}

// brokenSchedules is a schedule repository that fails the calls named in its fields.
type brokenSchedules struct {
	domain.ScheduleRepository
	get, insert, update, list, del, due error
}

func (r *brokenSchedules) Get(ctx context.Context, id string) (domain.Schedule, error) {
	if r.get != nil {
		return domain.Schedule{}, r.get
	}
	return r.ScheduleRepository.Get(ctx, id)
}

func (r *brokenSchedules) Insert(ctx context.Context, s domain.Schedule) error {
	if r.insert != nil {
		return r.insert
	}
	return r.ScheduleRepository.Insert(ctx, s)
}

func (r *brokenSchedules) Update(ctx context.Context, s domain.Schedule) error {
	if r.update != nil {
		return r.update
	}
	return r.ScheduleRepository.Update(ctx, s)
}

func (r *brokenSchedules) List(ctx context.Context, f domain.ScheduleFilter) ([]domain.Schedule, error) {
	if r.list != nil {
		return nil, r.list
	}
	return r.ScheduleRepository.List(ctx, f)
}

func (r *brokenSchedules) Delete(ctx context.Context, id string) error {
	if r.del != nil {
		return r.del
	}
	return r.ScheduleRepository.Delete(ctx, id)
}

func (r *brokenSchedules) Due(ctx context.Context, now time.Time) ([]domain.Schedule, error) {
	if r.due != nil {
		return nil, r.due
	}
	return r.ScheduleRepository.Due(ctx, now)
}

// brokenChart is an agent directory whose chart cannot be read.
type brokenChart struct{ core.AgentDirectory }

func (brokenChart) IsBelow(context.Context, string, string) (bool, error) { return false, errBoom }

// brokenFreezer is a freezer whose state cannot be read.
type brokenFreezer struct{ core.Freezer }

func (brokenFreezer) State(context.Context) ([]core.FreezeScope, error) { return nil, errBoom }

func (e *env) deps() app.Deps {
	return app.Deps{Clock: e.fakes.Clock, IDs: e.fakes.IDs, Ports: e.ports, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

// withBrokenTasks is a task service over a repository that fails on demand.
func (e *env) withBrokenTasks() (*app.Tasks, *brokenTasks) {
	repo := &brokenTasks{TaskRepository: memory.NewTasks()}
	return app.NewTasks(e.deps(), repo), repo
}

func (e *env) withBrokenSchedules() (*app.Schedules, *brokenSchedules) {
	repo := &brokenSchedules{ScheduleRepository: memory.NewSchedules()}
	return app.NewSchedules(e.deps(), repo), repo
}

func TestEveryOperationNeedsACaller(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()
	calls := map[string]func() error{
		"tasks.Create":     func() error { _, err := e.tasks.Create(ctx, app.CreateInput{Title: "x"}); return err },
		"tasks.Assign":     func() error { _, err := e.tasks.Assign(ctx, "tsk_1", "builder"); return err },
		"tasks.Complete":   func() error { _, err := e.tasks.Complete(ctx, "tsk_1", domain.StatusDone, "ok"); return err },
		"tasks.Cancel":     func() error { _, err := e.tasks.Cancel(ctx, "tsk_1"); return err },
		"tasks.Edit":       func() error { _, err := e.tasks.Edit(ctx, "tsk_1", domain.TaskEdit{}); return err },
		"tasks.Get":        func() error { _, err := e.tasks.Get(ctx, "tsk_1"); return err },
		"tasks.List":       func() error { _, err := e.tasks.List(ctx, domain.TaskFilter{}); return err },
		"schedules.Create": func() error { _, err := e.schedules.Create(ctx, app.CreateScheduleInput{}); return err },
		"schedules.Update": func() error { _, err := e.schedules.Update(ctx, "sch_1", domain.ScheduleEdit{}); return err },
		"schedules.Delete": func() error { return e.schedules.Delete(ctx, "sch_1") },
		"schedules.Get":    func() error { _, err := e.schedules.Get(ctx, "sch_1"); return err },
		"schedules.List":   func() error { _, err := e.schedules.List(ctx, domain.ScheduleFilter{}); return err },
		"port.GetTask":     func() error { _, err := e.tasks.GetTask(ctx, "tsk_1"); return err },
		"port.CompleteTask": func() error {
			_, err := e.tasks.CompleteTask(ctx, "tsk_1", core.TaskOutcome{Status: core.TaskDone, Result: "ok"})
			return err
		},
	}
	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if err := call(); !errors.Is(err, core.ErrUnauthorized) {
				t.Fatalf("error = %v, want ErrUnauthorized", err)
			}
		})
	}
}

func TestTasks_AFailingRepositoryFailsTheOperation(t *testing.T) {
	t.Parallel()
	ctx := asHuman("ada")
	seed := func(t *testing.T) (*app.Tasks, *brokenTasks, domain.Task) {
		t.Helper()
		e := newEnv(t)
		svc, repo := e.withBrokenTasks()
		task, err := svc.Create(ctx, app.CreateInput{Title: "Seed", Assignee: "builder"})
		if err != nil {
			t.Fatal(err)
		}
		return svc, repo, task
	}
	cases := map[string]func(t *testing.T, svc *app.Tasks, repo *brokenTasks, task domain.Task) error{
		"insert": func(_ *testing.T, svc *app.Tasks, repo *brokenTasks, _ domain.Task) error {
			repo.insert = errBoom
			_, err := svc.Create(ctx, app.CreateInput{Title: "New"})
			return err
		},
		"get": func(_ *testing.T, svc *app.Tasks, repo *brokenTasks, task domain.Task) error {
			repo.get = errBoom
			_, err := svc.Get(ctx, task.ID)
			return err
		},
		"list": func(_ *testing.T, svc *app.Tasks, repo *brokenTasks, _ domain.Task) error {
			repo.list = errBoom
			_, err := svc.List(ctx, domain.TaskFilter{})
			return err
		},
		"update on assign": func(_ *testing.T, svc *app.Tasks, repo *brokenTasks, task domain.Task) error {
			repo.update = errBoom
			_, err := svc.Assign(ctx, task.ID, "keeper")
			return err
		},
		"update on complete": func(_ *testing.T, svc *app.Tasks, repo *brokenTasks, task domain.Task) error {
			repo.update = errBoom
			_, err := svc.Complete(ctx, task.ID, domain.StatusDone, "ok")
			return err
		},
		"update on cancel": func(_ *testing.T, svc *app.Tasks, repo *brokenTasks, task domain.Task) error {
			repo.update = errBoom
			_, err := svc.Cancel(ctx, task.ID)
			return err
		},
		"update on edit": func(_ *testing.T, svc *app.Tasks, repo *brokenTasks, task domain.Task) error {
			repo.update = errBoom
			title := "Renamed"
			_, err := svc.Edit(ctx, task.ID, domain.TaskEdit{Title: &title})
			return err
		},
	}
	for name, run := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			svc, repo, task := seed(t)
			if err := run(t, svc, repo, task); !errors.Is(err, errBoom) {
				t.Fatalf("error = %v, want the repository's error", err)
			}
		})
	}
}

func TestTasks_AFailedUpdateIsNotAuditedOrAnnounced(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	svc, repo := e.withBrokenTasks()
	task, err := svc.Create(asHuman("ada"), app.CreateInput{Title: "Seed", Assignee: "builder"})
	if err != nil {
		t.Fatal(err)
	}
	before := len(e.auditActions())
	repo.update = errBoom
	if _, err := svc.Cancel(asHuman("ada"), task.ID); err == nil {
		t.Fatal("Cancel() succeeded, want an error")
	}
	if got := len(e.auditActions()); got != before {
		t.Fatalf("audit entries = %d, want %d", got, before)
	}
}

func TestTasks_AnUnreadableChartFailsTheOperation(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	svc := app.NewTasks(e.deps(), memory.NewTasks())
	task, err := svc.Create(asAgent("builder"), app.CreateInput{Title: "Seed", Assignee: "scribe"})
	if err != nil {
		t.Fatal(err)
	}
	e.ports.Agents = brokenChart{AgentDirectory: e.ports.Agents}
	agent := asAgent("keeper")
	for name, call := range map[string]func() error{
		"create": func() error { _, err := svc.Create(agent, app.CreateInput{Title: "x", Assignee: "scribe"}); return err },
		"assign": func() error { _, err := svc.Assign(agent, task.ID, "scribe"); return err },
		"complete": func() error {
			_, err := svc.Complete(agent, task.ID, domain.StatusDone, "ok")
			return err
		},
		"cancel": func() error { _, err := svc.Cancel(agent, task.ID); return err },
		"edit":   func() error { _, err := svc.Edit(agent, task.ID, domain.TaskEdit{}); return err },
	} {
		if err := call(); !errors.Is(err, errBoom) {
			t.Errorf("%s: error = %v, want the chart's error", name, err)
		}
	}
}

func TestTasks_UnreadableFreezesFailAnAssignment(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.ports.Freezer = brokenFreezer{Freezer: e.ports.Freezer}
	_, err := e.tasks.Create(asHuman("ada"), app.CreateInput{Title: "x", Assignee: "builder"})
	if !errors.Is(err, errBoom) {
		t.Fatalf("error = %v, want the freezer's error", err)
	}
}

func TestSchedules_AFailingRepositoryFailsTheOperation(t *testing.T) {
	t.Parallel()
	ctx := asHuman("ada")
	seed := func(t *testing.T) (*app.Schedules, *brokenSchedules, domain.Schedule) {
		t.Helper()
		e := newEnv(t)
		svc, repo := e.withBrokenSchedules()
		s, err := svc.Create(ctx, app.CreateScheduleInput{Agent: "builder", Name: "nightly", Calendar: daily9am, Message: "Go"})
		if err != nil {
			t.Fatal(err)
		}
		return svc, repo, s
	}
	cases := map[string]func(svc *app.Schedules, repo *brokenSchedules, s domain.Schedule) error{
		"insert": func(svc *app.Schedules, repo *brokenSchedules, _ domain.Schedule) error {
			repo.insert = errBoom
			_, err := svc.Create(ctx, app.CreateScheduleInput{Agent: "builder", Name: "two", Calendar: daily9am, Message: "Go"})
			return err
		},
		"get": func(svc *app.Schedules, repo *brokenSchedules, s domain.Schedule) error {
			repo.get = errBoom
			_, err := svc.Get(ctx, s.ID)
			return err
		},
		"list": func(svc *app.Schedules, repo *brokenSchedules, _ domain.Schedule) error {
			repo.list = errBoom
			_, err := svc.List(ctx, domain.ScheduleFilter{})
			return err
		},
		"update": func(svc *app.Schedules, repo *brokenSchedules, s domain.Schedule) error {
			repo.update = errBoom
			off := false
			_, err := svc.Update(ctx, s.ID, domain.ScheduleEdit{Enabled: &off})
			return err
		},
		"delete": func(svc *app.Schedules, repo *brokenSchedules, s domain.Schedule) error {
			repo.del = errBoom
			return svc.Delete(ctx, s.ID)
		},
		"due": func(svc *app.Schedules, repo *brokenSchedules, _ domain.Schedule) error {
			repo.due = errBoom
			_, err := svc.Tick(context.Background())
			return err
		},
	}
	for name, run := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			svc, repo, s := seed(t)
			if err := run(svc, repo, s); !errors.Is(err, errBoom) {
				t.Fatalf("error = %v, want the repository's error", err)
			}
		})
	}
}

func TestSchedules_AnUnreadableChartFailsTheOperation(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	s := e.newSchedule(t, "scribe")
	e.ports.Agents = brokenChart{AgentDirectory: e.ports.Agents}
	agent := asAgent("keeper")
	if _, err := e.schedules.Create(agent, app.CreateScheduleInput{Agent: "scribe", Name: "x", Calendar: daily9am, Message: "Go"}); !errors.Is(err, errBoom) {
		t.Errorf("Create() error = %v, want the chart's error", err)
	}
	if err := e.schedules.Delete(agent, s.ID); !errors.Is(err, errBoom) {
		t.Errorf("Delete() error = %v, want the chart's error", err)
	}
}

func TestTick_AFailedSaveAfterTheWakeIsReported(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	svc, repo := e.withBrokenSchedules()
	if _, err := svc.Create(asHuman("ada"), app.CreateScheduleInput{Agent: "keeper", Name: "nightly", Calendar: daily9am, Message: "Go"}); err != nil {
		t.Fatal(err)
	}
	e.fakes.Clock.Advance(48 * time.Hour)
	repo.update = errBoom

	fired, err := svc.Tick(context.Background())

	if !errors.Is(err, errBoom) || fired != 0 {
		t.Fatalf("Tick() = %d, %v, want 0 and the repository's error", fired, err)
	}
}

func TestTick_UnreadableFreezesStopTheFire(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.newSchedule(t, "keeper")
	e.fakes.Clock.Advance(48 * time.Hour)
	e.ports.Freezer = brokenFreezer{Freezer: e.ports.Freezer}

	fired, err := e.schedules.Tick(context.Background())

	if !errors.Is(err, errBoom) || fired != 0 || len(e.wakedAgents()) != 0 {
		t.Fatalf("Tick() = %d, %v with %d wakes, want 0, the freezer's error and no wake", fired, err, len(e.wakedAgents()))
	}
}
