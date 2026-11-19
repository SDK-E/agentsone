// SPDX-License-Identifier: MIT

// Package schedtest holds builders and the repository contracts for the
// sched context: one suite every domain.TaskRepository and
// domain.ScheduleRepository implementation must pass, in memory and on SQLite.
package schedtest

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/SDK-E/agentsone/internal/sched/domain"
)

// Epoch is the time the builders start from.
var Epoch = time.Date(2026, 11, 19, 8, 0, 0, 0, time.UTC)

// ID returns a task or schedule id with a stable, sortable suffix.
func ID(prefix string, n int) string { return fmt.Sprintf("%s_%026d", prefix, n) }

// Task builds a valid open task with id tsk_<n>. Change fields on the result.
func Task(n int) domain.Task {
	t, err := domain.NewTask(domain.NewTaskParams{
		ID: ID("tsk", n), Title: fmt.Sprintf("Task %d", n), Detail: "Detail",
		Source: domain.SourceAgent, CreatedBy: domain.Actor{Kind: domain.ActorAgent, Name: "builder"},
		Now: Epoch.Add(time.Duration(n) * time.Minute),
	})
	if err != nil {
		panic(err)
	}
	return t
}

// Schedule builds a valid enabled daily schedule with id sch_<n>.
func Schedule(n int) domain.Schedule {
	s, err := domain.NewSchedule(domain.NewScheduleParams{
		ID: ID("sch", n), Agent: "founder", Name: fmt.Sprintf("Timer %d", n),
		Calendar: "*-*-* 09:00:00", Message: "Wake up.", Enabled: true,
		CreatedBy: domain.Actor{Kind: domain.ActorSystem, Name: "daemon"}, Now: Epoch,
	})
	if err != nil {
		panic(err)
	}
	return s
}

var diffOpts = []cmp.Option{cmp.Comparer(func(a, b domain.Calendar) bool { return a.String() == b.String() })}

func same[T any](t *testing.T, want, got T) {
	t.Helper()
	if diff := cmp.Diff(want, got, diffOpts...); diff != "" {
		t.Errorf("mismatch (-want +got):\n%s", diff)
	}
}

// RunTaskRepositoryContract runs the task repository contract against fresh
// repositories made by newRepo.
func RunTaskRepositoryContract(t *testing.T, newRepo func(t *testing.T) domain.TaskRepository) {
	t.Helper()
	t.Run("unknown id is not found", func(t *testing.T) {
		t.Parallel()
		if _, err := newRepo(t).Get(t.Context(), ID("tsk", 1)); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("Get() error = %v, want %v", err, domain.ErrNotFound)
		}
	})
	t.Run("a task round trips with every field", func(t *testing.T) {
		t.Parallel()
		repo := newRepo(t)
		task := Task(1)
		due := Epoch.Add(48 * time.Hour)
		task.Assignee, task.ParentID, task.TriggerID, task.DueAt = "scribe", ID("tsk", 9), ID("trg", 3), &due
		task.Priority = domain.PriorityHigh
		if err := task.Complete(domain.StatusDone, "Wrote it.", Epoch.Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		if err := repo.Insert(t.Context(), task); err != nil {
			t.Fatalf("Insert() error = %v", err)
		}
		got, err := repo.Get(t.Context(), task.ID)
		if err != nil {
			t.Fatalf("Get() error = %v", err)
		}
		same(t, task, got)
	})
	t.Run("a task with no optional fields round trips", func(t *testing.T) {
		t.Parallel()
		repo := newRepo(t)
		if err := repo.Insert(t.Context(), Task(1)); err != nil {
			t.Fatal(err)
		}
		got, err := repo.Get(t.Context(), ID("tsk", 1))
		if err != nil {
			t.Fatal(err)
		}
		same(t, Task(1), got)
	})
	t.Run("inserting an existing id conflicts", func(t *testing.T) {
		t.Parallel()
		repo := newRepo(t)
		if err := repo.Insert(t.Context(), Task(1)); err != nil {
			t.Fatal(err)
		}
		if err := repo.Insert(t.Context(), Task(1)); !errors.Is(err, domain.ErrConflict) {
			t.Fatalf("Insert() error = %v, want %v", err, domain.ErrConflict)
		}
	})
	t.Run("update replaces the stored task", func(t *testing.T) {
		t.Parallel()
		repo := newRepo(t)
		task := Task(1)
		if err := repo.Insert(t.Context(), task); err != nil {
			t.Fatal(err)
		}
		if err := task.Assign("scribe", Epoch.Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		if err := repo.Update(t.Context(), task); err != nil {
			t.Fatalf("Update() error = %v", err)
		}
		got, _ := repo.Get(t.Context(), task.ID)
		same(t, task, got)
	})
	t.Run("update of an unknown id is not found", func(t *testing.T) {
		t.Parallel()
		if err := newRepo(t).Update(t.Context(), Task(1)); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("Update() error = %v, want %v", err, domain.ErrNotFound)
		}
	})
	t.Run("list is newest first and filters", func(t *testing.T) { listTasks(t, newRepo(t)) })
	t.Run("list pages with a before id", func(t *testing.T) { pageTasks(t, newRepo(t)) })
}

func taskIDs(tasks []domain.Task) []string {
	ids := make([]string, 0, len(tasks))
	for _, t := range tasks {
		ids = append(ids, t.ID)
	}
	return ids
}

func listTasks(t *testing.T, repo domain.TaskRepository) {
	t.Helper()
	a, b, c := Task(1), Task(2), Task(3)
	a.Assignee = "scribe"
	b.Assignee, b.Source = "builder", domain.SourceHuman
	if err := c.Cancel(Epoch); err != nil {
		t.Fatal(err)
	}
	for _, task := range []domain.Task{a, b, c} {
		if err := repo.Insert(t.Context(), task); err != nil {
			t.Fatal(err)
		}
	}
	tests := []struct {
		name string
		f    domain.TaskFilter
		want []string
	}{
		{"everything, newest first", domain.TaskFilter{}, []string{c.ID, b.ID, a.ID}},
		{"by status", domain.TaskFilter{Status: domain.StatusCancelled}, []string{c.ID}},
		{"by assignee", domain.TaskFilter{Assignee: "scribe"}, []string{a.ID}},
		{"by source", domain.TaskFilter{Source: domain.SourceHuman}, []string{b.ID}},
		{"combined filters that match nothing", domain.TaskFilter{Assignee: "scribe", Source: domain.SourceHuman}, []string{}},
	}
	for _, tt := range tests {
		got, err := repo.List(t.Context(), tt.f)
		if err != nil {
			t.Fatalf("%s: List() error = %v", tt.name, err)
		}
		if diff := cmp.Diff(tt.want, taskIDs(got)); diff != "" {
			t.Errorf("%s (-want +got):\n%s", tt.name, diff)
		}
	}
}

func pageTasks(t *testing.T, repo domain.TaskRepository) {
	t.Helper()
	for n := 1; n <= 5; n++ {
		if err := repo.Insert(t.Context(), Task(n)); err != nil {
			t.Fatal(err)
		}
	}
	got, err := repo.List(t.Context(), domain.TaskFilter{BeforeID: ID("tsk", 4), Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff([]string{ID("tsk", 3), ID("tsk", 2)}, taskIDs(got)); diff != "" {
		t.Errorf("page (-want +got):\n%s", diff)
	}
}

// RunScheduleRepositoryContract runs the schedule repository contract
// against fresh repositories made by newRepo.
func RunScheduleRepositoryContract(t *testing.T, newRepo func(t *testing.T) domain.ScheduleRepository) {
	t.Helper()
	t.Run("unknown id is not found", func(t *testing.T) {
		t.Parallel()
		if _, err := newRepo(t).Get(t.Context(), ID("sch", 1)); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("Get() error = %v, want %v", err, domain.ErrNotFound)
		}
	})
	t.Run("a schedule round trips with every field", func(t *testing.T) {
		t.Parallel()
		repo := newRepo(t)
		s := Schedule(1)
		if _, err := s.Fire(*s.NextRun); err != nil {
			t.Fatal(err)
		}
		if err := repo.Insert(t.Context(), s); err != nil {
			t.Fatalf("Insert() error = %v", err)
		}
		got, err := repo.Get(t.Context(), s.ID)
		if err != nil {
			t.Fatalf("Get() error = %v", err)
		}
		same(t, s, got)
	})
	t.Run("a disabled schedule round trips without next run", func(t *testing.T) {
		t.Parallel()
		repo := newRepo(t)
		s := Schedule(1)
		off := false
		if err := s.Edit(domain.ScheduleEdit{Enabled: &off}, Epoch); err != nil {
			t.Fatal(err)
		}
		if err := repo.Insert(t.Context(), s); err != nil {
			t.Fatal(err)
		}
		got, _ := repo.Get(t.Context(), s.ID)
		same(t, s, got)
	})
	t.Run("inserting an existing id conflicts", func(t *testing.T) {
		t.Parallel()
		repo := newRepo(t)
		if err := repo.Insert(t.Context(), Schedule(1)); err != nil {
			t.Fatal(err)
		}
		if err := repo.Insert(t.Context(), Schedule(1)); !errors.Is(err, domain.ErrConflict) {
			t.Fatalf("Insert() error = %v, want %v", err, domain.ErrConflict)
		}
	})
	t.Run("update replaces and unknown id is not found", func(t *testing.T) {
		t.Parallel()
		repo := newRepo(t)
		s := Schedule(1)
		if err := repo.Update(t.Context(), s); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("Update() error = %v, want %v", err, domain.ErrNotFound)
		}
		if err := repo.Insert(t.Context(), s); err != nil {
			t.Fatal(err)
		}
		name := "Renamed"
		if err := s.Edit(domain.ScheduleEdit{Name: &name}, Epoch); err != nil {
			t.Fatal(err)
		}
		if err := repo.Update(t.Context(), s); err != nil {
			t.Fatal(err)
		}
		got, _ := repo.Get(t.Context(), s.ID)
		same(t, s, got)
	})
	t.Run("delete removes and unknown id is not found", func(t *testing.T) {
		t.Parallel()
		repo := newRepo(t)
		if err := repo.Delete(t.Context(), ID("sch", 1)); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("Delete() error = %v, want %v", err, domain.ErrNotFound)
		}
		if err := repo.Insert(t.Context(), Schedule(1)); err != nil {
			t.Fatal(err)
		}
		if err := repo.Delete(t.Context(), ID("sch", 1)); err != nil {
			t.Fatal(err)
		}
		if _, err := repo.Get(t.Context(), ID("sch", 1)); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("Get() after delete error = %v, want %v", err, domain.ErrNotFound)
		}
	})
	t.Run("list is oldest first, filters and pages", func(t *testing.T) { listSchedules(t, newRepo(t)) })
	t.Run("due returns only enabled schedules at or past their time", func(t *testing.T) { dueSchedules(t, newRepo(t)) })
}

func scheduleIDs(list []domain.Schedule) []string {
	ids := make([]string, 0, len(list))
	for _, s := range list {
		ids = append(ids, s.ID)
	}
	return ids
}

func listSchedules(t *testing.T, repo domain.ScheduleRepository) {
	t.Helper()
	for n := 1; n <= 3; n++ {
		s := Schedule(n)
		if n == 2 {
			s.Agent = "builder"
		}
		if err := repo.Insert(t.Context(), s); err != nil {
			t.Fatal(err)
		}
	}
	tests := []struct {
		name string
		f    domain.ScheduleFilter
		want []string
	}{
		{"everything, oldest first", domain.ScheduleFilter{}, []string{ID("sch", 1), ID("sch", 2), ID("sch", 3)}},
		{"by agent", domain.ScheduleFilter{Agent: "builder"}, []string{ID("sch", 2)}},
		{"a page after an id", domain.ScheduleFilter{AfterID: ID("sch", 1), Limit: 1}, []string{ID("sch", 2)}},
		{"agent that has none", domain.ScheduleFilter{Agent: "nobody"}, []string{}},
	}
	for _, tt := range tests {
		got, err := repo.List(t.Context(), tt.f)
		if err != nil {
			t.Fatalf("%s: List() error = %v", tt.name, err)
		}
		if diff := cmp.Diff(tt.want, scheduleIDs(got)); diff != "" {
			t.Errorf("%s (-want +got):\n%s", tt.name, diff)
		}
	}
}

func dueSchedules(t *testing.T, repo domain.ScheduleRepository) {
	t.Helper()
	early, late, off := Schedule(1), Schedule(2), Schedule(3)
	cal := "*-*-* 12:00:00"
	if err := late.Edit(domain.ScheduleEdit{Calendar: &cal}, Epoch); err != nil {
		t.Fatal(err)
	}
	disabled := false
	if err := off.Edit(domain.ScheduleEdit{Enabled: &disabled}, Epoch); err != nil {
		t.Fatal(err)
	}
	for _, s := range []domain.Schedule{early, late, off} {
		if err := repo.Insert(t.Context(), s); err != nil {
			t.Fatal(err)
		}
	}
	tests := []struct {
		name string
		now  time.Time
		want []string
	}{
		{"before any run", early.NextRun.Add(-time.Second), []string{}},
		{"exactly at the first run", *early.NextRun, []string{early.ID}},
		{"after both runs", late.NextRun.Add(time.Hour), []string{early.ID, late.ID}},
	}
	for _, tt := range tests {
		got, err := repo.Due(t.Context(), tt.now)
		if err != nil {
			t.Fatalf("%s: Due() error = %v", tt.name, err)
		}
		if diff := cmp.Diff(tt.want, scheduleIDs(got)); diff != "" {
			t.Errorf("%s (-want +got):\n%s", tt.name, diff)
		}
	}
}
