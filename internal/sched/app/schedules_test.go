// SPDX-License-Identifier: MIT

package app_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/sched/app"
	"github.com/SDK-E/agentsone/internal/sched/domain"
)

const (
	unknownSchedule = "sch_00000000000000000000000000"
	daily9am        = "*-*-* 09:00:00"
)

var epoch = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// failingEvents is an EventPublisher that cannot publish.
type failingEvents struct{ err error }

func (p failingEvents) Publish(context.Context, core.Event) (core.Event, error) {
	return core.Event{}, p.err
}

func (e *env) newSchedule(t *testing.T, agent string) domain.Schedule {
	t.Helper()
	s, err := e.schedules.Create(asHuman("ada"), app.CreateScheduleInput{Agent: agent, Name: "nightly-review", Calendar: daily9am, Message: "Review the day"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	return s
}

func TestCreateSchedule_DefaultsToTheCallerAndPlansTheFirstRun(t *testing.T) {
	t.Parallel()
	e := newEnv(t)

	got, err := e.schedules.Create(asAgent("keeper"), app.CreateScheduleInput{Name: "nightly-review", Calendar: daily9am, Message: "Review"})

	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if got.Agent != "keeper" || !got.Enabled || got.CreatedBy.String() != "agent:keeper" {
		t.Fatalf("schedule = %+v, want an enabled timer of keeper", got)
	}
	if want := epoch.Add(9 * time.Hour); got.NextRun == nil || !got.NextRun.Equal(want) {
		t.Errorf("next run = %v, want %v", got.NextRun, want)
	}
	if diff := cmp.Diff([]string{"schedule.create"}, e.auditActions()); diff != "" {
		t.Errorf("audit (-want +got):\n%s", diff)
	}
}

func TestCreateSchedule_AnAgentAboveMayCreateForOneBelow(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	off := false

	got, err := e.schedules.Create(asAgent("builder"), app.CreateScheduleInput{Agent: "scribe", Name: "x", Calendar: "hourly", Enabled: &off})

	if err != nil || got.Agent != "scribe" || got.Enabled || got.NextRun != nil {
		t.Fatalf("Create() = %+v, %v, want a disabled timer of scribe with no next run", got, err)
	}
}

func TestCreateSchedule_Refusals(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		caller  context.Context
		in      app.CreateScheduleInput
		wantErr error
	}{
		{"agent is a peer", asAgent("keeper"), app.CreateScheduleInput{Agent: "peer", Name: "x", Calendar: "hourly"}, core.ErrForbidden},
		{"agent is above", asAgent("scribe"), app.CreateScheduleInput{Agent: "builder", Name: "x", Calendar: "hourly"}, core.ErrForbidden},
		{"agent does not exist", asHuman("ada"), app.CreateScheduleInput{Agent: "ghost", Name: "x", Calendar: "hourly"}, core.ErrNotFound},
		{"a human names no agent", asHuman("ada"), app.CreateScheduleInput{Name: "x", Calendar: "hourly"}, core.ErrInvalid},
		{"calendar is not OnCalendar", asAgent("keeper"), app.CreateScheduleInput{Name: "x", Calendar: "every day"}, core.ErrInvalid},
		{"calendar never fires", asAgent("keeper"), app.CreateScheduleInput{Name: "x", Calendar: "2020-01-01 00:00:00"}, core.ErrInvalid},
		{"name is empty", asAgent("keeper"), app.CreateScheduleInput{Name: "", Calendar: "hourly"}, core.ErrInvalid},
		{"no caller", context.Background(), app.CreateScheduleInput{Agent: "keeper", Name: "x", Calendar: "hourly"}, core.ErrUnauthorized},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t)

			_, err := e.schedules.Create(tt.caller, tt.in)

			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Create() error = %v, want %v", err, tt.wantErr)
			}
			if list, _ := e.schedules.List(asHuman("ada"), domain.ScheduleFilter{}); len(list) != 0 || len(e.auditActions()) != 0 {
				t.Errorf("stored %v and audited %v after a refusal", list, e.auditActions())
			}
		})
	}
}

func TestUpdateSchedule_ChangesTheTimerAndPlansAgain(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	s := e.newSchedule(t, "scribe")
	cal, name, off := "*-*-* 17:30:00", "evening", false

	got, err := e.schedules.Update(asAgent("builder"), s.ID, domain.ScheduleEdit{Calendar: &cal, Name: &name})
	if err != nil || got.Name != "evening" || got.NextRun == nil || !got.NextRun.Equal(epoch.Add(17*time.Hour+30*time.Minute)) {
		t.Fatalf("Update() = %+v, %v", got, err)
	}
	got, err = e.schedules.Update(asAgent("scribe"), s.ID, domain.ScheduleEdit{Enabled: &off})
	if err != nil || got.Enabled || got.NextRun != nil {
		t.Fatalf("Update(disable) = %+v, %v", got, err)
	}
	if diff := cmp.Diff([]string{"schedule.create", "schedule.update", "schedule.update"}, e.auditActions()); diff != "" {
		t.Errorf("audit (-want +got):\n%s", diff)
	}
}

func TestUpdateSchedule_Refusals(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	s := e.newSchedule(t, "scribe")
	name, bad := "x", "never"
	if _, err := e.schedules.Update(asAgent("keeper"), s.ID, domain.ScheduleEdit{Name: &name}); !errors.Is(err, core.ErrForbidden) {
		t.Errorf("stranger error = %v, want %v", err, core.ErrForbidden)
	}
	if _, err := e.schedules.Update(asAgent("scribe"), s.ID, domain.ScheduleEdit{Calendar: &bad}); !errors.Is(err, core.ErrInvalid) {
		t.Errorf("bad calendar error = %v, want %v", err, core.ErrInvalid)
	}
	if _, err := e.schedules.Update(asAgent("scribe"), unknownSchedule, domain.ScheduleEdit{Name: &name}); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("unknown id error = %v, want %v", err, core.ErrNotFound)
	}
	if got, _ := e.schedules.Get(asAgent("scribe"), s.ID); got.Name != "nightly-review" {
		t.Errorf("name = %q after refusals, want it unchanged", got.Name)
	}
	if diff := cmp.Diff([]string{"schedule.create"}, e.auditActions()); diff != "" {
		t.Errorf("audit (-want +got):\n%s", diff)
	}
}

func TestDeleteSchedule_RemovesItForItsOwnerOrAnAgentAbove(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	s := e.newSchedule(t, "scribe")
	if err := e.schedules.Delete(asAgent("keeper"), s.ID); !errors.Is(err, core.ErrForbidden) {
		t.Fatalf("stranger error = %v, want %v", err, core.ErrForbidden)
	}

	if err := e.schedules.Delete(asAgent("founder"), s.ID); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	if _, err := e.schedules.Get(asAgent("scribe"), s.ID); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("Get() after delete error = %v, want %v", err, core.ErrNotFound)
	}
	if err := e.schedules.Delete(asAgent("founder"), s.ID); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("second Delete() error = %v, want %v", err, core.ErrNotFound)
	}
	if diff := cmp.Diff([]string{"schedule.create", "schedule.delete"}, e.auditActions()); diff != "" {
		t.Errorf("audit (-want +got):\n%s", diff)
	}
}

func TestListSchedules_FiltersByAgentOldestFirst(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	a := e.newSchedule(t, "scribe")
	b := e.newSchedule(t, "keeper")
	c := e.newSchedule(t, "scribe")

	all, err := e.schedules.List(asAgent("peer"), domain.ScheduleFilter{})
	if err != nil {
		t.Fatal(err)
	}
	mine, _ := e.schedules.List(asAgent("peer"), domain.ScheduleFilter{Agent: "scribe"})

	if diff := cmp.Diff([]string{a.ID, b.ID, c.ID}, ids(all)); diff != "" {
		t.Errorf("all (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{a.ID, c.ID}, ids(mine)); diff != "" {
		t.Errorf("scribe's (-want +got):\n%s", diff)
	}
}

func ids(list []domain.Schedule) []string {
	var out []string
	for _, s := range list {
		out = append(out, s.ID)
	}
	return out
}

func TestTick_FiringWakesTheOwnerPublishesTheEventAndPlansTheNextRun(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	s := e.newSchedule(t, "keeper")
	e.fakes.Clock.Advance(9*time.Hour + 2*time.Second)

	fired, err := e.schedules.Tick(context.Background())

	if err != nil || fired != 1 {
		t.Fatalf("Tick() = %d, %v, want 1 fired", fired, err)
	}
	wakes := e.fakes.Waker.Wakes()
	if len(wakes) != 1 || wakes[0].Agent != "keeper" || wakes[0].Reason != core.WakeTimer {
		t.Fatalf("wakes = %+v, want one timer wake of keeper", wakes)
	}
	wantPayload := map[string]any{
		"scheduleId": s.ID, "name": "nightly-review", "due": "2026-01-01T09:00:00Z",
		"firedAt": "2026-01-01T09:00:02Z", "note": "Review the day",
	}
	if diff := cmp.Diff(wantPayload, payloadOf(t, wakes[0].Payload)); diff != "" {
		t.Errorf("payload (-want +got):\n%s", diff)
	}
	events := e.fakes.Events.Published()
	if len(events) != 1 {
		t.Fatalf("published %d events, want 1", len(events))
	}
	ev := events[0]
	if ev.Source != "/emitters/clock" || ev.Type != "agentsone.clock.schedule.fired" || ev.Subject != s.ID ||
		ev.Actor != "system:clock" || ev.ChartScope != "agent:keeper" || ev.DataContentType != "application/json" {
		t.Errorf("event = %+v", ev)
	}
	var data map[string]any
	if err := json.Unmarshal(ev.Data, &data); err != nil {
		t.Fatal(err)
	}
	wantData := map[string]any{"agent": "keeper", "schedule_id": s.ID, "name": "nightly-review", "due": "2026-01-01T09:00:00Z"}
	if diff := cmp.Diff(wantData, data); diff != "" {
		t.Errorf("event data (-want +got):\n%s", diff)
	}
	got, _ := e.schedules.Get(asAgent("keeper"), s.ID)
	if got.LastRun == nil || got.NextRun == nil || !got.NextRun.Equal(epoch.Add(33*time.Hour)) {
		t.Errorf("schedule = %+v, want last run set and next run at 09:00 the next day", got)
	}
	last := e.fakes.Audit.Entries()[1]
	if last.Action != "schedule.fire" || last.Actor != "system:clock" || last.Target != "schedule:"+s.ID {
		t.Errorf("audit entry = %+v, want schedule.fire by system:clock", last)
	}
}

func TestTick_DoesNothingBeforeTheTime(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.newSchedule(t, "keeper")
	e.fakes.Clock.Advance(8 * time.Hour)

	fired, err := e.schedules.Tick(context.Background())

	if err != nil || fired != 0 || len(e.wakedAgents()) != 0 || len(e.fakes.Events.Published()) != 0 {
		t.Fatalf("Tick() = %d, %v with wakes %v, want nothing", fired, err, e.wakedAgents())
	}
}

func TestTick_RunsMissedDuringDowntimeFireOnce(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.newSchedule(t, "keeper")
	e.fakes.Clock.Advance(3*24*time.Hour + 10*time.Hour) // down for three days

	first, err := e.schedules.Tick(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	second, err := e.schedules.Tick(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if first != 1 || second != 0 || len(e.wakedAgents()) != 1 {
		t.Fatalf("fired %d then %d with %d wakes, want one fire in all", first, second, len(e.wakedAgents()))
	}
}

func TestTick_SkipsDisabledSchedules(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	s := e.newSchedule(t, "keeper")
	off := false
	if _, err := e.schedules.Update(asAgent("keeper"), s.ID, domain.ScheduleEdit{Enabled: &off}); err != nil {
		t.Fatal(err)
	}
	e.fakes.Clock.Advance(48 * time.Hour)

	if fired, err := e.schedules.Tick(context.Background()); err != nil || fired != 0 {
		t.Fatalf("Tick() = %d, %v, want nothing", fired, err)
	}
}

func TestTick_NeverWakesAFrozenAgentAndFiresOnceAfterTheThaw(t *testing.T) {
	t.Parallel()
	for _, scope := range []string{"agent:scribe", "team:builder", "all"} {
		t.Run(scope, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t)
			s := e.newSchedule(t, "scribe")
			e.freeze(t, scope)
			e.fakes.Clock.Advance(10 * time.Hour)

			fired, err := e.schedules.Tick(context.Background())

			if err != nil || fired != 0 || len(e.wakedAgents()) != 0 || len(e.fakes.Events.Published()) != 0 {
				t.Fatalf("Tick() = %d, %v, wakes %v, want nothing while frozen", fired, err, e.wakedAgents())
			}
			if got, _ := e.schedules.Get(asAgent("scribe"), s.ID); got.LastRun != nil {
				t.Errorf("last run = %v, want none", got.LastRun)
			}

			scopeValue, _ := core.ParseFreezeScope(scope)
			if err := e.fakes.Freezer.Release(context.Background(), scopeValue); err != nil {
				t.Fatal(err)
			}
			if fired, err := e.schedules.Tick(context.Background()); err != nil || fired != 1 {
				t.Fatalf("Tick() after the thaw = %d, %v, want 1", fired, err)
			}
		})
	}
}

func TestTick_AFreezeThatLandsBetweenTheCheckAndTheWakeSkipsTheFire(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	s := e.newSchedule(t, "keeper")
	e.fakes.Waker.Freeze("keeper")
	e.fakes.Clock.Advance(10 * time.Hour)

	fired, err := e.schedules.Tick(context.Background())

	if err != nil || fired != 0 || len(e.fakes.Events.Published()) != 0 {
		t.Fatalf("Tick() = %d, %v, want the fire skipped", fired, err)
	}
	if got, _ := e.schedules.Get(asAgent("keeper"), s.ID); got.LastRun != nil {
		t.Errorf("last run = %v, want none so the fire stays pending", got.LastRun)
	}
}

func TestTick_AFailedWakeIsRetriedOnTheNextTick(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	s := e.newSchedule(t, "keeper")
	e.fakes.Clock.Advance(10 * time.Hour)
	good := e.ports.Waker
	e.ports.Waker = failingWaker{err: errors.New("runner is down")}

	fired, err := e.schedules.Tick(context.Background())
	if err == nil || fired != 0 {
		t.Fatalf("Tick() = %d, %v, want an error and no fire", fired, err)
	}
	if got, _ := e.schedules.Get(asAgent("keeper"), s.ID); got.LastRun != nil {
		t.Fatalf("last run = %v, want none so the fire is retried", got.LastRun)
	}

	e.ports.Waker = good
	if fired, err := e.schedules.Tick(context.Background()); err != nil || fired != 1 {
		t.Fatalf("Tick() on retry = %d, %v, want 1", fired, err)
	}
}

func TestTick_AFailedPublishDoesNotUndoTheFire(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	s := e.newSchedule(t, "keeper")
	e.fakes.Clock.Advance(10 * time.Hour)
	e.ports.Events = failingEvents{err: errors.New("bus is down")}

	fired, err := e.schedules.Tick(context.Background())

	if err != nil || fired != 1 || len(e.wakedAgents()) != 1 {
		t.Fatalf("Tick() = %d, %v with %d wakes, want the fire kept", fired, err, len(e.wakedAgents()))
	}
	if got, _ := e.schedules.Get(asAgent("keeper"), s.ID); got.LastRun == nil {
		t.Error("last run is empty, want it recorded so the agent is not woken twice")
	}
	var failed []core.AuditEntry
	for _, a := range e.fakes.Audit.Entries() {
		if a.Outcome == core.AuditFailure {
			failed = append(failed, a)
		}
	}
	if len(failed) != 1 || failed[0].Action != "event.publish" || failed[0].Target != "schedule:"+s.ID ||
		!strings.Contains(string(failed[0].Detail), "bus is down") {
		t.Fatalf("failure entries = %+v, want one event.publish failure for the schedule", failed)
	}
}

func TestTick_AnAgentGoneFromTheChartIsSkipped(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.newSchedule(t, "keeper")
	e.ports.Agents = emptyDirectory{}
	e.fakes.Clock.Advance(10 * time.Hour)

	fired, err := e.schedules.Tick(context.Background())

	if err != nil || fired != 0 || len(e.wakedAgents()) != 0 {
		t.Fatalf("Tick() = %d, %v, want nothing", fired, err)
	}
}

// emptyDirectory is an AgentDirectory that knows nobody.
type emptyDirectory struct{ core.AgentDirectory }

func (emptyDirectory) GetAgent(context.Context, string) (core.Agent, error) {
	return core.Agent{}, core.ErrNotFound
}
