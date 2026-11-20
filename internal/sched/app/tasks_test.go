// SPDX-License-Identifier: MIT

package app_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/sched/app"
	"github.com/SDK-E/agentsone/internal/sched/domain"
)

const unknownTask = "tsk_00000000000000000000000000"

func TestCreate_StoresTheTaskAndRecordsTheCreator(t *testing.T) {
	t.Parallel()
	e := newEnv(t)

	got, err := e.tasks.Create(asAgent("builder"), app.CreateInput{Title: "Write docs", Detail: "Budget page"})

	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if got.Status != domain.StatusOpen || got.CreatedBy.String() != "agent:builder" || got.Source != domain.SourceAgent {
		t.Fatalf("task = %+v, want open, created by agent:builder, source agent", got)
	}
	stored, err := e.tasks.Get(asAgent("peer"), got.ID)
	if err != nil || stored.Title != "Write docs" || stored.Detail != "Budget page" {
		t.Fatalf("Get() = %+v, %v", stored, err)
	}
	if diff := cmp.Diff([]string{"task.create"}, e.auditActions()); diff != "" {
		t.Errorf("audit (-want +got):\n%s", diff)
	}
	if len(e.wakedAgents()) != 0 {
		t.Errorf("woke %v, want nobody for an unassigned task", e.wakedAgents())
	}
}

func TestCreate_ByAHumanHasSourceHuman(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	got, err := e.tasks.Create(asHuman("ada"), app.CreateInput{Title: "Review"})
	if err != nil || got.Source != domain.SourceHuman || got.CreatedBy.String() != "human:ada" {
		t.Fatalf("Create() = %+v, %v", got, err)
	}
}

func TestCreate_UnderAParentAndWithADueTime(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	parent, _ := e.tasks.Create(asAgent("builder"), app.CreateInput{Title: "Parent"})
	due := e.fakes.Clock.Now().Add(time.Hour)

	got, err := e.tasks.Create(asAgent("builder"), app.CreateInput{Title: "Child", ParentID: parent.ID, DueAt: &due})

	if err != nil || got.ParentID != parent.ID || got.DueAt == nil || !got.DueAt.Equal(due) {
		t.Fatalf("Create() = %+v, %v", got, err)
	}
}

func TestCreate_AssigningWakesTheAssigneeWithReasonTask(t *testing.T) {
	t.Parallel()
	e := newEnv(t)

	got, err := e.tasks.Create(asAgent("builder"), app.CreateInput{Title: "Write docs", Detail: "Budget page", Assignee: "scribe"})

	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	wakes := e.fakes.Waker.Wakes()
	if len(wakes) != 1 || wakes[0].Agent != "scribe" || wakes[0].Reason != core.WakeTask {
		t.Fatalf("wakes = %+v, want one task wake of scribe", wakes)
	}
	want := map[string]any{
		"id": got.ID, "title": "Write docs", "body": "Budget page", "from": "agent:builder",
		"createdAt": "2026-01-01T00:00:00Z",
	}
	if diff := cmp.Diff(want, payloadOf(t, wakes[0].Payload)); diff != "" {
		t.Errorf("payload (-want +got):\n%s", diff)
	}
}

func TestCreate_TheTaskWakeCarriesTheDueTime(t *testing.T) {
	t.Parallel()
	e := newEnv(t) // the fixture's waker checks the payload against the runner contract
	due := time.Date(2026, 1, 2, 15, 4, 5, 0, time.FixedZone("CET", 3600))

	if _, err := e.tasks.Create(asAgent("builder"), app.CreateInput{Title: "Ship it", Assignee: "scribe", DueAt: &due}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	wakes := e.fakes.Waker.Wakes()
	if len(wakes) != 1 {
		t.Fatalf("wakes = %+v, want one", wakes)
	}
	if got := payloadOf(t, wakes[0].Payload)["due"]; got != "2026-01-02T14:04:05Z" {
		t.Fatalf("due = %v, want the UTC time", got)
	}
}

func TestCreate_RefusesWithoutTouchingAnything(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		caller  string
		in      app.CreateInput
		freeze  string
		wantErr error
	}{
		{"assignee is not below the caller", "scribe", app.CreateInput{Title: "x", Assignee: "keeper"}, "", core.ErrForbidden},
		{"assignee does not exist", "builder", app.CreateInput{Title: "x", Assignee: "ghost"}, "", core.ErrNotFound},
		{"assignee is frozen", "builder", app.CreateInput{Title: "x", Assignee: "scribe"}, "agent:scribe", core.ErrFrozen},
		{"assignee is in a frozen team", "founder", app.CreateInput{Title: "x", Assignee: "scribe"}, "team:builder", core.ErrFrozen},
		{"everything is frozen", "founder", app.CreateInput{Title: "x", Assignee: "keeper"}, "all", core.ErrFrozen},
		{"parent does not exist", "builder", app.CreateInput{Title: "x", ParentID: unknownTask}, "", core.ErrNotFound},
		{"title is empty", "builder", app.CreateInput{Title: " "}, "", core.ErrInvalid},
		{"priority is unknown", "builder", app.CreateInput{Title: "x", Priority: "urgent"}, "", core.ErrInvalid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t)
			if tt.freeze != "" {
				e.freeze(t, tt.freeze)
			}

			_, err := e.tasks.Create(asAgent(tt.caller), tt.in)

			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Create() error = %v, want %v", err, tt.wantErr)
			}
			list, lerr := e.tasks.List(asHuman("ada"), domain.TaskFilter{})
			if lerr != nil || len(list) != 0 {
				t.Errorf("tasks after a refusal = %v, %v, want none", list, lerr)
			}
			if len(e.auditActions()) != 0 || len(e.wakedAgents()) != 0 {
				t.Errorf("audit %v and wakes %v after a refusal, want none", e.auditActions(), e.wakedAgents())
			}
		})
	}
}

func TestCreate_AHumanMayAssignToAnyAgent(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	if _, err := e.tasks.Create(asHuman("ada"), app.CreateInput{Title: "x", Assignee: "scribe"}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
}

func TestCreate_NeedsACaller(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	if _, err := e.tasks.Create(context.Background(), app.CreateInput{Title: "x"}); !errors.Is(err, core.ErrUnauthorized) {
		t.Fatalf("Create() error = %v, want %v", err, core.ErrUnauthorized)
	}
}

func TestCreate_AnAgentGoneFromTheChartCannotAssign(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	_, err := e.tasks.Create(asAgent("ghost"), app.CreateInput{Title: "x", Assignee: "scribe"})
	if !errors.Is(err, core.ErrForbidden) {
		t.Fatalf("Create() error = %v, want %v", err, core.ErrForbidden)
	}
}

func TestAssign_GivesTheTaskToAnAgentBelowAndWakesIt(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	task, _ := e.tasks.Create(asAgent("builder"), app.CreateInput{Title: "x"})

	got, err := e.tasks.Assign(asAgent("builder"), task.ID, "scribe")

	if err != nil || got.Assignee != "scribe" {
		t.Fatalf("Assign() = %+v, %v", got, err)
	}
	if diff := cmp.Diff([]string{"scribe"}, e.wakedAgents()); diff != "" {
		t.Errorf("wakes (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"task.create", "task.assign"}, e.auditActions()); diff != "" {
		t.Errorf("audit (-want +got):\n%s", diff)
	}
}

func TestAssign_AgentMayTakeATaskItself(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	task, _ := e.tasks.Create(asAgent("builder"), app.CreateInput{Title: "x"})
	if got, err := e.tasks.Assign(asAgent("keeper"), task.ID, "keeper"); err != nil || got.Assignee != "keeper" {
		t.Fatalf("Assign() = %+v, %v", got, err)
	}
}

func TestAssign_Refusals(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		caller   string
		assignee string
		close    bool
		freeze   string
		wantErr  error
	}{
		{"assignee is a peer", "builder", "keeper", false, "", core.ErrForbidden},
		{"assignee is above the caller", "scribe", "builder", false, "", core.ErrForbidden},
		{"assignee does not exist", "builder", "ghost", false, "", core.ErrNotFound},
		{"task is closed", "builder", "scribe", true, "", core.ErrConflict},
		{"assignee is frozen", "builder", "scribe", false, "agent:scribe", core.ErrFrozen},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t)
			task, _ := e.tasks.Create(asAgent("builder"), app.CreateInput{Title: "x"})
			if tt.close {
				if _, err := e.tasks.Cancel(asAgent("builder"), task.ID); err != nil {
					t.Fatal(err)
				}
			}
			if tt.freeze != "" {
				e.freeze(t, tt.freeze)
			}
			audited := len(e.auditActions())

			_, err := e.tasks.Assign(asAgent(tt.caller), task.ID, tt.assignee)

			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Assign() error = %v, want %v", err, tt.wantErr)
			}
			got, _ := e.tasks.Get(asHuman("ada"), task.ID)
			if got.Assignee != "" {
				t.Errorf("assignee = %q after a refusal", got.Assignee)
			}
			if len(e.auditActions()) != audited || len(e.wakedAgents()) != 0 {
				t.Errorf("audit grew to %v or woke %v after a refusal", e.auditActions(), e.wakedAgents())
			}
		})
	}
}

func TestAssign_UnknownTaskIsNotFound(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	if _, err := e.tasks.Assign(asAgent("builder"), unknownTask, "scribe"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("Assign() error = %v, want %v", err, core.ErrNotFound)
	}
}

func TestAssign_KeepsTheAssignmentWhenTheWakeFindsTheAgentFrozen(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	task, _ := e.tasks.Create(asAgent("builder"), app.CreateInput{Title: "x"})
	e.fakes.Waker.Freeze("scribe") // frozen between the check and the wake

	got, err := e.tasks.Assign(asAgent("builder"), task.ID, "scribe")

	if err != nil || got.Assignee != "scribe" {
		t.Fatalf("Assign() = %+v, %v, want the assignment kept", got, err)
	}
	if diff := cmp.Diff([]string{"task.create", "task.assign", "task.wake.skipped"}, e.auditActions()); diff != "" {
		t.Errorf("audit (-want +got):\n%s", diff)
	}
}

func TestAssign_KeepsTheAssignmentWhenTheWakeFails(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	task, _ := e.tasks.Create(asAgent("builder"), app.CreateInput{Title: "x"})
	e.ports.Waker = failingWaker{err: errors.New("runner is down")}

	got, err := e.tasks.Assign(asAgent("builder"), task.ID, "scribe")

	if err != nil || got.Assignee != "scribe" {
		t.Fatalf("Assign() = %+v, %v, want the assignment kept", got, err)
	}
	if diff := cmp.Diff([]string{"task.create", "task.assign", "task.wake.failed"}, e.auditActions()); diff != "" {
		t.Errorf("audit (-want +got):\n%s", diff)
	}
	entries := e.fakes.Audit.Entries()
	if last := entries[len(entries)-1]; last.Outcome != core.AuditFailure || !strings.Contains(string(last.Detail), "runner is down") {
		t.Errorf("task.wake.failed entry = %+v (%s), want outcome failure with the cause", last, last.Detail)
	}
}

func TestComplete_HolderReportsTheOutcome(t *testing.T) {
	t.Parallel()
	for _, outcome := range []domain.Status{domain.StatusDone, domain.StatusFailed} {
		e := newEnv(t)
		task, _ := e.tasks.Create(asAgent("builder"), app.CreateInput{Title: "x", Assignee: "scribe"})

		got, err := e.tasks.Complete(asAgent("scribe"), task.ID, outcome, "wrote it")

		if err != nil || got.Status != outcome || got.Result != "wrote it" {
			t.Fatalf("Complete(%s) = %+v, %v", outcome, got, err)
		}
		if last := e.auditActions(); last[len(last)-1] != "task.complete" {
			t.Errorf("audit = %v, want task.complete last", last)
		}
	}
}

func TestComplete_AnAgentAboveTheHolderMay(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	task, _ := e.tasks.Create(asAgent("builder"), app.CreateInput{Title: "x", Assignee: "scribe"})
	if _, err := e.tasks.Complete(asAgent("founder"), task.ID, domain.StatusDone, "ok"); err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
}

func TestComplete_Refusals(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		caller  string
		outcome domain.Status
		result  string
		close   bool
		wantErr error
	}{
		{"a peer of the holder", "keeper", domain.StatusDone, "ok", false, core.ErrForbidden},
		{"already closed", "scribe", domain.StatusDone, "ok", true, core.ErrConflict},
		{"empty result", "scribe", domain.StatusDone, "", false, core.ErrInvalid},
		{"outcome cancelled", "scribe", domain.StatusCancelled, "ok", false, core.ErrInvalid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t)
			task, _ := e.tasks.Create(asAgent("builder"), app.CreateInput{Title: "x", Assignee: "scribe"})
			if tt.close {
				if _, err := e.tasks.Complete(asAgent("scribe"), task.ID, domain.StatusDone, "first"); err != nil {
					t.Fatal(err)
				}
			}
			audited := len(e.auditActions())

			_, err := e.tasks.Complete(asAgent(tt.caller), task.ID, tt.outcome, tt.result)

			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Complete() error = %v, want %v", err, tt.wantErr)
			}
			if len(e.auditActions()) != audited {
				t.Errorf("audit grew to %v after a refusal", e.auditActions())
			}
		})
	}
}

func TestComplete_AnUnassignedTaskHasNoHolder(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	task, _ := e.tasks.Create(asAgent("builder"), app.CreateInput{Title: "x"})
	if _, err := e.tasks.Complete(asAgent("founder"), task.ID, domain.StatusDone, "ok"); !errors.Is(err, core.ErrForbidden) {
		t.Fatalf("Complete() error = %v, want %v", err, core.ErrForbidden)
	}
}

func TestCancel_CreatorCancelsTheTask(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	task, _ := e.tasks.Create(asAgent("builder"), app.CreateInput{Title: "x", Assignee: "scribe"})

	got, err := e.tasks.Cancel(asAgent("builder"), task.ID)

	if err != nil || got.Status != domain.StatusCancelled {
		t.Fatalf("Cancel() = %+v, %v", got, err)
	}
	if diff := cmp.Diff([]string{"task.create", "task.cancel"}, e.auditActions()); diff != "" {
		t.Errorf("audit (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"scribe"}, e.wakedAgents()); diff != "" {
		t.Errorf("wakes (-want +got):\n%s, want only the wake of the assignment", diff)
	}
}

func TestCancel_AnAgentAboveTheCreatorMay(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	task, _ := e.tasks.Create(asAgent("builder"), app.CreateInput{Title: "x"})
	if _, err := e.tasks.Cancel(asAgent("founder"), task.ID); err != nil {
		t.Fatalf("Cancel() error = %v", err)
	}
}

func TestCancel_Refusals(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	task, _ := e.tasks.Create(asAgent("builder"), app.CreateInput{Title: "x", Assignee: "scribe"})
	if _, err := e.tasks.Cancel(asAgent("scribe"), task.ID); !errors.Is(err, core.ErrForbidden) {
		t.Fatalf("Cancel() by the assignee error = %v, want %v", err, core.ErrForbidden)
	}
	if _, err := e.tasks.Cancel(asHuman("ada"), task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.tasks.Cancel(asHuman("ada"), task.ID); !errors.Is(err, core.ErrConflict) {
		t.Fatalf("second Cancel() error = %v, want %v", err, core.ErrConflict)
	}
}

func TestCancel_ATaskCreatedByAHumanNeedsAHuman(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	task, _ := e.tasks.Create(asHuman("ada"), app.CreateInput{Title: "x"})
	if _, err := e.tasks.Cancel(asAgent("founder"), task.ID); !errors.Is(err, core.ErrForbidden) {
		t.Fatalf("Cancel() error = %v, want %v", err, core.ErrForbidden)
	}
}

func TestEdit_ChangesWordingAndPriority(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	task, _ := e.tasks.Create(asAgent("builder"), app.CreateInput{Title: "x", Assignee: "scribe"})
	title, prio := "New title", domain.PriorityHigh

	got, err := e.tasks.Edit(asAgent("scribe"), task.ID, domain.TaskEdit{Title: &title, Priority: &prio})

	if err != nil || got.Title != "New title" || got.Priority != domain.PriorityHigh {
		t.Fatalf("Edit() = %+v, %v", got, err)
	}
	if last := e.auditActions(); last[len(last)-1] != "task.edit" {
		t.Errorf("audit = %v, want task.edit last", last)
	}
}

func TestEdit_Refusals(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	task, _ := e.tasks.Create(asAgent("builder"), app.CreateInput{Title: "x", Assignee: "scribe"})
	title, empty := "y", ""
	if _, err := e.tasks.Edit(asAgent("keeper"), task.ID, domain.TaskEdit{Title: &title}); !errors.Is(err, core.ErrForbidden) {
		t.Errorf("Edit() by a stranger error = %v, want %v", err, core.ErrForbidden)
	}
	if _, err := e.tasks.Edit(asAgent("builder"), task.ID, domain.TaskEdit{Title: &empty}); !errors.Is(err, core.ErrInvalid) {
		t.Errorf("Edit() with an empty title error = %v, want %v", err, core.ErrInvalid)
	}
	if _, err := e.tasks.Cancel(asAgent("builder"), task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.tasks.Edit(asAgent("builder"), task.ID, domain.TaskEdit{Title: &title}); !errors.Is(err, core.ErrConflict) {
		t.Errorf("Edit() of a closed task error = %v, want %v", err, core.ErrConflict)
	}
}

func TestEdit_UnknownTaskIsNotFound(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	title := "y"
	if _, err := e.tasks.Edit(asAgent("builder"), unknownTask, domain.TaskEdit{Title: &title}); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("Edit() error = %v, want %v", err, core.ErrNotFound)
	}
}

func TestGet_UnknownTaskIsNotFound(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	if _, err := e.tasks.Get(asAgent("builder"), unknownTask); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("Get() error = %v, want %v", err, core.ErrNotFound)
	}
}

func TestList_FiltersByStatusAssigneeAndSource(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	a, _ := e.tasks.Create(asAgent("builder"), app.CreateInput{Title: "a", Assignee: "scribe"})
	b, _ := e.tasks.Create(asHuman("ada"), app.CreateInput{Title: "b"})
	if _, err := e.tasks.Cancel(asHuman("ada"), b.ID); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		f    domain.TaskFilter
		want []string
	}{
		{"all, newest first", domain.TaskFilter{}, []string{b.ID, a.ID}},
		{"by status", domain.TaskFilter{Status: domain.StatusCancelled}, []string{b.ID}},
		{"by assignee", domain.TaskFilter{Assignee: "scribe"}, []string{a.ID}},
		{"by source", domain.TaskFilter{Source: domain.SourceHuman}, []string{b.ID}},
	}
	for _, tt := range tests {
		got, err := e.tasks.List(asAgent("peer"), tt.f)
		if err != nil {
			t.Fatalf("%s: List() error = %v", tt.name, err)
		}
		var ids []string
		for _, task := range got {
			ids = append(ids, task.ID)
		}
		if diff := cmp.Diff(tt.want, ids); diff != "" {
			t.Errorf("%s (-want +got):\n%s", tt.name, diff)
		}
	}
}

func TestSubscribe_StreamsEveryChangeAsAFrame(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	frames := e.tasks.Subscribe(t.Context(), "")

	task, _ := e.tasks.Create(asAgent("builder"), app.CreateInput{Title: "x"})
	if _, err := e.tasks.Assign(asAgent("builder"), task.ID, "scribe"); err != nil {
		t.Fatal(err)
	}

	first, second := <-frames, <-frames
	if first.Task.ID != task.ID || first.Task.Assignee != "" || second.Task.Assignee != "scribe" {
		t.Fatalf("frames = %+v, %+v", first, second)
	}
	if first.ID >= second.ID {
		t.Errorf("frame ids %q, %q do not increase", first.ID, second.ID)
	}
}

func TestSubscribe_ReplaysFramesAfterTheLastEventID(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	live := e.tasks.Subscribe(t.Context(), "")
	a, _ := e.tasks.Create(asAgent("builder"), app.CreateInput{Title: "a"})
	b, _ := e.tasks.Create(asAgent("builder"), app.CreateInput{Title: "b"})
	firstFrame := <-live
	<-live

	replay := e.tasks.Subscribe(t.Context(), firstFrame.ID)

	got := <-replay
	if got.Task.ID != b.ID || got.Task.ID == a.ID {
		t.Fatalf("replayed task %s, want only the one after the last event id (%s)", got.Task.ID, b.ID)
	}
}

func TestSubscribe_ClosesWhenTheContextEnds(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx, cancel := context.WithCancel(t.Context())
	frames := e.tasks.Subscribe(ctx, "")

	cancel()

	select {
	case _, ok := <-frames:
		if ok {
			t.Fatal("got a frame after the context ended")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("channel still open 5s after the context ended")
	}
}

func TestSubscribe_DropsASubscriberThatFallsBehind(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	frames := e.tasks.Subscribe(t.Context(), "")

	for range 400 {
		if _, err := e.tasks.Create(asAgent("builder"), app.CreateInput{Title: "x"}); err != nil {
			t.Fatal(err)
		}
	}

	n := 0
	for range frames { // ends because the hub closed the channel
		n++
	}
	if n == 0 || n >= 400 {
		t.Fatalf("read %d frames, want some but fewer than were sent", n)
	}
}

func TestPort_CreateTaskTakesTheCreatorFromTheRequest(t *testing.T) {
	t.Parallel()
	e := newEnv(t)

	created, err := e.ports.Tasks.CreateTask(asAgent("builder"), core.NewTask{Title: "From port", Body: "Detail", Assignee: "scribe", Priority: "high"})

	if err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	if created.Status != "open" || created.Priority != "high" || created.CreatedBy != "agent:builder" || created.Assignee != "scribe" {
		t.Fatalf("task = %+v", created)
	}
	if diff := cmp.Diff([]string{"scribe"}, e.wakedAgents()); diff != "" {
		t.Errorf("wakes (-want +got):\n%s", diff)
	}
}

func TestPort_CreateTaskFromAnEventHasSourceTrigger(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := core.WithCaller(context.Background(), core.Caller{Kind: core.CallerSystem, Name: "triggers", UID: -1})

	created, err := e.ports.Tasks.CreateTask(ctx, core.NewTask{
		Title: "Disk is full", Assignee: "keeper", CauseEventID: "evt_1", CreatedBy: "system:triggers",
	})

	if err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	got, _ := e.tasks.Get(ctx, created.ID)
	if got.Source != domain.SourceTrigger || got.CreatedBy.String() != "system:triggers" || got.TriggerID != "evt_1" {
		t.Fatalf("task = %+v, want source trigger by system:triggers caused by evt_1", got)
	}
}

func TestPort_CreateTaskSourceFollowsTheSystemPart(t *testing.T) {
	t.Parallel()
	tests := map[string]domain.Source{
		"system:router": domain.SourceRouter,
		"system:clock":  domain.SourceSchedule,
		"system:other":  domain.SourceTrigger,
	}
	for by, want := range tests {
		e := newEnv(t)
		ctx := core.WithCaller(context.Background(), core.Caller{Kind: core.CallerSystem, Name: strings.TrimPrefix(by, "system:"), UID: -1})
		created, err := e.ports.Tasks.CreateTask(ctx, core.NewTask{Title: "x", CreatedBy: by})
		if err != nil {
			t.Fatalf("%s: CreateTask() error = %v", by, err)
		}
		if got, _ := e.tasks.Get(ctx, created.ID); got.Source != want {
			t.Errorf("%s: source = %s, want %s", by, got.Source, want)
		}
	}
}

func TestPort_ADaemonPartWithoutACallerMayNameItselfButNotAnAgent(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	created, err := e.ports.Tasks.CreateTask(context.Background(), core.NewTask{Title: "x", CreatedBy: "system:triggers", CauseEventID: "evt_1"})
	if err != nil || created.CreatedBy != "system:triggers" {
		t.Fatalf("CreateTask() = %+v, %v", created, err)
	}
	if _, err := e.ports.Tasks.CreateTask(context.Background(), core.NewTask{Title: "x", CreatedBy: "agent:builder"}); !errors.Is(err, core.ErrUnauthorized) {
		t.Errorf("agent without a caller error = %v, want %v", err, core.ErrUnauthorized)
	}
	if _, err := e.ports.Tasks.CreateTask(context.Background(), core.NewTask{Title: "x"}); !errors.Is(err, core.ErrUnauthorized) {
		t.Errorf("nobody error = %v, want %v", err, core.ErrUnauthorized)
	}
}

func TestPort_AnAgentCannotCreateTasksInSomeoneElsesName(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	created, err := e.ports.Tasks.CreateTask(asAgent("builder"), core.NewTask{Title: "x", CreatedBy: "agent:founder"})
	if err != nil || created.CreatedBy != "agent:builder" {
		t.Fatalf("CreateTask() = %+v, %v, want the creator to be the caller", created, err)
	}
}

func TestPort_CreateTaskRejectsABadCreatorAndABadPriority(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := core.WithCaller(context.Background(), core.Caller{Kind: core.CallerSystem, Name: "triggers", UID: -1})
	if _, err := e.ports.Tasks.CreateTask(ctx, core.NewTask{Title: "x", CreatedBy: "nobody"}); !errors.Is(err, core.ErrInvalid) {
		t.Errorf("bad creator error = %v, want %v", err, core.ErrInvalid)
	}
	if _, err := e.ports.Tasks.CreateTask(asAgent("builder"), core.NewTask{Title: "x", Priority: "urgent"}); !errors.Is(err, core.ErrInvalid) {
		t.Errorf("bad priority error = %v, want %v", err, core.ErrInvalid)
	}
}

func TestPort_CompleteTaskAsTheHolderReportsDone(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	task, _ := e.tasks.Create(asAgent("builder"), app.CreateInput{Title: "x", Assignee: "scribe"})

	got, err := e.ports.Tasks.CompleteTask(asAgent("scribe"), task.ID, core.TaskOutcome{Status: core.TaskDone, Result: "ok"})

	if err != nil || got.Status != "done" || got.Result != "ok" {
		t.Fatalf("CompleteTask() = %+v, %v", got, err)
	}
	read, err := e.ports.Tasks.GetTask(asAgent("scribe"), task.ID)
	if err != nil || read.Status != "done" || read.ID != task.ID || read.Title != "x" {
		t.Fatalf("GetTask() = %+v, %v", read, err)
	}
}

func TestPort_CompleteTaskWithCancelledCancelsIt(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	task, _ := e.tasks.Create(asAgent("builder"), app.CreateInput{Title: "x", Assignee: "scribe"})
	got, err := e.ports.Tasks.CompleteTask(asAgent("builder"), task.ID, core.TaskOutcome{Status: core.TaskCancelled})
	if err != nil || got.Status != "cancelled" {
		t.Fatalf("CompleteTask() = %+v, %v", got, err)
	}
}

func TestPort_CompleteTaskRejectsABadOutcomeAndAStranger(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	task, _ := e.tasks.Create(asAgent("builder"), app.CreateInput{Title: "x", Assignee: "scribe"})
	if _, err := e.ports.Tasks.CompleteTask(asAgent("scribe"), task.ID, core.TaskOutcome{Status: "open"}); !errors.Is(err, core.ErrInvalid) {
		t.Errorf("bad outcome error = %v, want %v", err, core.ErrInvalid)
	}
	if _, err := e.ports.Tasks.CompleteTask(asAgent("keeper"), task.ID, core.TaskOutcome{Status: core.TaskDone, Result: "x"}); !errors.Is(err, core.ErrForbidden) {
		t.Errorf("stranger error = %v, want %v", err, core.ErrForbidden)
	}
}

func TestErrors_NameTheFieldAndKeepTheCoreKind(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	_, err := e.tasks.Create(asAgent("builder"), app.CreateInput{Title: strings.Repeat("x", 201)})
	if !errors.Is(err, core.ErrInvalid) || !strings.Contains(err.Error(), "title") || strings.Contains(err.Error(), "invalid: invalid") {
		t.Fatalf("error = %v, want core.ErrInvalid that names the title once", err)
	}
}

func TestAudit_AFailingAuditLogDoesNotUndoTheChange(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.ports.Audit = failingAudit{err: errors.New("disk full")}

	got, err := e.tasks.Create(asAgent("builder"), app.CreateInput{Title: "x"})

	if err != nil {
		t.Fatalf("Create() error = %v, want the change to stand", err)
	}
	if _, err := e.tasks.Get(asAgent("builder"), got.ID); err != nil {
		t.Fatalf("Get() error = %v", err)
	}
}
