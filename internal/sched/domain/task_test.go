// SPDX-License-Identifier: MIT

package domain_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/sched/domain"
)

var epoch = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func agent(name string) domain.Actor { return domain.Actor{Kind: domain.ActorAgent, Name: name} }

func human(name string) domain.Actor { return domain.Actor{Kind: domain.ActorHuman, Name: name} }

func newTask(t *testing.T, opts ...func(*domain.NewTaskParams)) domain.Task {
	t.Helper()
	p := domain.NewTaskParams{
		ID: "tsk_1", Title: "Write the budget page", Source: domain.SourceAgent,
		CreatedBy: agent("builder"), Now: epoch,
	}
	for _, o := range opts {
		o(&p)
	}
	task, err := domain.NewTask(p)
	if err != nil {
		t.Fatalf("NewTask() error = %v", err)
	}
	return task
}

func TestNewTask_StartsOpenWithDefaults(t *testing.T) {
	t.Parallel()
	task := newTask(t)
	if task.Status != domain.StatusOpen || task.Priority != domain.PriorityNormal || task.Assignee != "" {
		t.Fatalf("task = %+v, want open, normal, unassigned", task)
	}
	if !task.CreatedAt.Equal(epoch) || !task.UpdatedAt.Equal(epoch) {
		t.Fatalf("times = %v, %v, want %v", task.CreatedAt, task.UpdatedAt, epoch)
	}
}

func TestNewTask_RejectsBadFields(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		opt  func(*domain.NewTaskParams)
	}{
		{"empty title", func(p *domain.NewTaskParams) { p.Title = "  " }},
		{"title of 201 characters", func(p *domain.NewTaskParams) { p.Title = strings.Repeat("x", 201) }},
		{"detail of 16001 characters", func(p *domain.NewTaskParams) { p.Detail = strings.Repeat("x", 16001) }},
		{"unknown priority", func(p *domain.NewTaskParams) { p.Priority = "urgent" }},
		{"unknown source", func(p *domain.NewTaskParams) { p.Source = "dream" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p := domain.NewTaskParams{ID: "tsk_1", Title: "ok", Source: domain.SourceAgent, CreatedBy: agent("a"), Now: epoch}
			tt.opt(&p)
			if _, err := domain.NewTask(p); !errors.Is(err, domain.ErrInvalid) {
				t.Fatalf("NewTask() error = %v, want %v", err, domain.ErrInvalid)
			}
		})
	}
}

func TestNewTask_AcceptsFieldsOnTheLimit(t *testing.T) {
	t.Parallel()
	_, err := domain.NewTask(domain.NewTaskParams{
		ID: "tsk_1", Title: strings.Repeat("é", 200), Detail: strings.Repeat("x", 16000),
		Source: domain.SourceHuman, CreatedBy: human("ada"), Now: epoch,
	})
	if err != nil {
		t.Fatalf("NewTask() error = %v", err)
	}
}

func TestTaskAssign(t *testing.T) {
	t.Parallel()
	later := epoch.Add(time.Hour)
	task := newTask(t)
	if err := task.Assign("scribe", later); err != nil {
		t.Fatalf("Assign() error = %v", err)
	}
	if task.Assignee != "scribe" || task.Status != domain.StatusOpen || !task.UpdatedAt.Equal(later) {
		t.Fatalf("task = %+v", task)
	}
}

func TestTaskAssign_ResetsAStartedTaskToOpen(t *testing.T) {
	t.Parallel()
	task := newTask(t)
	task.Status = domain.StatusInProgress
	if err := task.Assign("scribe", epoch); err != nil {
		t.Fatal(err)
	}
	if task.Status != domain.StatusOpen {
		t.Fatalf("status = %q, want open", task.Status)
	}
}

func TestClosedTask_RefusesEveryChange(t *testing.T) {
	t.Parallel()
	for _, status := range []domain.Status{domain.StatusDone, domain.StatusCancelled, domain.StatusFailed} {
		t.Run(string(status), func(t *testing.T) {
			t.Parallel()
			task := newTask(t)
			task.Status = status
			title := "new"
			changes := map[string]error{
				"assign":   task.Assign("scribe", epoch),
				"complete": task.Complete(domain.StatusDone, "ok", epoch),
				"cancel":   task.Cancel(epoch),
				"edit":     task.Edit(domain.TaskEdit{Title: &title}, epoch),
			}
			for name, err := range changes {
				if !errors.Is(err, domain.ErrClosed) {
					t.Errorf("%s error = %v, want %v", name, err, domain.ErrClosed)
				}
			}
			if task.Status != status || task.Title == "new" {
				t.Fatalf("closed task changed: %+v", task)
			}
		})
	}
}

func TestTaskComplete(t *testing.T) {
	t.Parallel()
	for _, outcome := range []domain.Status{domain.StatusDone, domain.StatusFailed} {
		task := newTask(t)
		if err := task.Complete(outcome, "wrote it", epoch.Add(time.Minute)); err != nil {
			t.Fatalf("Complete(%s) error = %v", outcome, err)
		}
		if task.Status != outcome || task.Result != "wrote it" {
			t.Fatalf("task = %+v, want %s with result", task, outcome)
		}
	}
}

func TestTaskComplete_RejectsBadInput(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		outcome domain.Status
		result  string
	}{
		{"outcome cancelled", domain.StatusCancelled, "x"},
		{"outcome open", domain.StatusOpen, "x"},
		{"empty result", domain.StatusDone, " "},
		{"result of 16001 characters", domain.StatusDone, strings.Repeat("x", 16001)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			task := newTask(t)
			if err := task.Complete(tt.outcome, tt.result, epoch); !errors.Is(err, domain.ErrInvalid) {
				t.Fatalf("Complete() error = %v, want %v", err, domain.ErrInvalid)
			}
			if task.Status != domain.StatusOpen {
				t.Fatalf("status = %q after refusal", task.Status)
			}
		})
	}
}

func TestTaskCancel(t *testing.T) {
	t.Parallel()
	task := newTask(t)
	if err := task.Cancel(epoch); err != nil || task.Status != domain.StatusCancelled {
		t.Fatalf("Cancel() = %v, status %q", err, task.Status)
	}
}

func TestTaskEdit_ChangesOnlyTheGivenFields(t *testing.T) {
	t.Parallel()
	task := newTask(t)
	detail, prio := "more", domain.PriorityHigh
	if err := task.Edit(domain.TaskEdit{Detail: &detail, Priority: &prio}, epoch.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if task.Title != "Write the budget page" || task.Detail != "more" || task.Priority != domain.PriorityHigh {
		t.Fatalf("task = %+v", task)
	}
}

func TestTaskEdit_RejectsBadFields(t *testing.T) {
	t.Parallel()
	empty, bad := "", domain.Priority("urgent")
	long := strings.Repeat("x", 16001)
	for name, e := range map[string]domain.TaskEdit{
		"empty title": {Title: &empty}, "bad priority": {Priority: &bad}, "long detail": {Detail: &long},
	} {
		task := newTask(t)
		if err := task.Edit(e, epoch); !errors.Is(err, domain.ErrInvalid) {
			t.Errorf("%s: Edit() error = %v, want %v", name, err, domain.ErrInvalid)
		}
	}
}

func TestParseActor(t *testing.T) {
	t.Parallel()
	got, err := domain.ParseActor("human:ada")
	if err != nil || got != human("ada") || got.String() != "human:ada" {
		t.Fatalf("ParseActor() = %+v, %v", got, err)
	}
	for _, bad := range []string{"", "ada", "robot:x", "agent:"} {
		if _, err := domain.ParseActor(bad); !errors.Is(err, domain.ErrInvalid) {
			t.Errorf("ParseActor(%q) error = %v, want %v", bad, err, domain.ErrInvalid)
		}
	}
}

func TestCanAssign(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		actor    domain.Actor
		standing domain.Standing
		wantErr  error
	}{
		{"agent assigns to itself", agent("a"), domain.Standing{Self: true}, nil},
		{"agent assigns to an agent below", agent("a"), domain.Standing{Above: true}, nil},
		{"agent assigns to a peer or above", agent("a"), domain.Standing{}, domain.ErrNotPermitted},
		{"human assigns to any agent", human("ada"), domain.Standing{}, nil},
		{"system assigns to any agent", domain.Actor{Kind: domain.ActorSystem, Name: "triggers"}, domain.Standing{}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if err := domain.CanAssign(tt.actor, tt.standing); !errors.Is(err, tt.wantErr) {
				t.Fatalf("CanAssign() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestTaskCanComplete(t *testing.T) {
	t.Parallel()
	assigned := func(tk *domain.Task) { tk.Assignee = "scribe" }
	tests := []struct {
		name    string
		assign  bool
		actor   domain.Actor
		holder  domain.Standing
		wantErr error
	}{
		{"holder completes", true, agent("scribe"), domain.Standing{Self: true}, nil},
		{"agent above the holder completes", true, agent("boss"), domain.Standing{Above: true}, nil},
		{"peer of the holder is refused", true, agent("peer"), domain.Standing{}, domain.ErrNotPermitted},
		{"human completes", true, human("ada"), domain.Standing{}, nil},
		{"unassigned task is refused to an agent", false, agent("a"), domain.Standing{}, domain.ErrNotPermitted},
		{"unassigned task is open to a human", false, human("ada"), domain.Standing{}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			task := newTask(t)
			if tt.assign {
				assigned(&task)
			}
			if err := task.CanComplete(tt.actor, tt.holder); !errors.Is(err, tt.wantErr) {
				t.Fatalf("CanComplete() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestTaskCanCancel(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		actor   domain.Actor
		creator domain.Standing
		wantErr error
	}{
		{"creator cancels", agent("builder"), domain.Standing{Self: true}, nil},
		{"agent above the creator cancels", agent("boss"), domain.Standing{Above: true}, nil},
		{"unrelated agent is refused", agent("other"), domain.Standing{}, domain.ErrNotPermitted},
		{"human cancels", human("ada"), domain.Standing{}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			task := newTask(t)
			if err := task.CanCancel(tt.actor, tt.creator); !errors.Is(err, tt.wantErr) {
				t.Fatalf("CanCancel() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestTaskCanEdit(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name              string
		actor             domain.Actor
		creator, assignee domain.Standing
		wantErr           error
	}{
		{"creator edits", agent("builder"), domain.Standing{Self: true}, domain.Standing{}, nil},
		{"assignee edits", agent("scribe"), domain.Standing{}, domain.Standing{Self: true}, nil},
		{"agent above the creator edits", agent("boss"), domain.Standing{Above: true}, domain.Standing{}, nil},
		{"agent above the assignee edits", agent("boss"), domain.Standing{}, domain.Standing{Above: true}, nil},
		{"unrelated agent is refused", agent("other"), domain.Standing{}, domain.Standing{}, domain.ErrNotPermitted},
		{"human edits", human("ada"), domain.Standing{}, domain.Standing{}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			task := newTask(t)
			task.Assignee = "scribe"
			if err := task.CanEdit(tt.actor, tt.creator, tt.assignee); !errors.Is(err, tt.wantErr) {
				t.Fatalf("CanEdit() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}
