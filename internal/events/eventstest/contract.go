// SPDX-License-Identifier: MIT

// Package eventstest holds the repository contracts of the events context:
// one suite per repository that the in-memory fake and the SQLite adapter
// must both pass, plus builders for test data.
package eventstest

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/events/domain"
)

// At is the time test data starts at.
var At = time.Date(2026, 11, 19, 10, 0, 0, 123456789, time.UTC)

// Event returns a valid stored-shape event with id suffix n (no sequence).
func Event(n int) core.Event {
	at := At.Add(time.Duration(n) * time.Second)
	return core.Event{
		SpecVersion: "1.0", ID: id("evt", n), Source: "/emitters/disk", Type: "agentsone.disk.usage.high",
		Subject: "/var", Time: &at, DataContentType: "application/json",
		DataSchema: "https://example.test/schema", Data: json.RawMessage(`{"mount":"/var"}`),
		Actor: "system:disk", ChartScope: "machine", CausationID: id("tsk", n), CorrelationID: id("evt", n),
	}
}

func id(prefix string, n int) string {
	const alphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
	suffix := []byte("00000000000000000000000000")
	suffix[25] = alphabet[n%32]
	suffix[24] = alphabet[n/32%32]
	return prefix + "_" + string(suffix)
}

// ID makes a prefixed id with numeric suffix n.
func ID(prefix string, n int) string { return id(prefix, n) }

// Emitter returns a valid process emitter named name with id suffix n.
func Emitter(n int, name string) domain.Emitter {
	return domain.Emitter{
		ID: id("emt", n), Name: name, Kind: domain.KindProcess, Description: "Test emitter.",
		EventTypes: []string{"agentsone." + name + ".*"}, Enabled: true, Unit: "emitter@" + id("emt", n) + ".service",
		Owner: "agent:builder", CreatedAt: At, Command: []string{"/bin/true", "--x"}, Secret: "s3cret",
	}
}

// Trigger returns a valid wake trigger with id suffix n.
func Trigger(n int, owner string) domain.Trigger {
	return domain.Trigger{
		ID: id("trg", n), Name: "disk alarm", Owner: owner,
		Filter: domain.Filter{
			Types: []string{"agentsone.disk.*"}, Sources: []string{"/emitters/disk"},
			SubjectPrefix: "/v", Actors: []string{"system:disk"},
		},
		Action:  domain.Action{Kind: domain.ActionWake, Agent: "janitor", Message: "look at {{subject}}"},
		Rate:    domain.TriggerRate{Max: 2, PerSeconds: 60},
		Enabled: true, CreatedAt: At,
	}
}

// RunEventContract runs the event repository contract on fresh repositories.
func RunEventContract(t *testing.T, newRepo func(t *testing.T) domain.EventRepository) {
	t.Helper()
	t.Run("empty log has sequence zero", func(t *testing.T) {
		last, err := newRepo(t).Last(t.Context())
		if err != nil || last != 0 {
			t.Fatalf("Last() = %d, %v, want 0", last, err)
		}
	})
	t.Run("append assigns increasing sequences and round trips", func(t *testing.T) {
		repo := newRepo(t)
		first, err := repo.Append(t.Context(), Event(1))
		if err != nil {
			t.Fatal(err)
		}
		second, err := repo.Append(t.Context(), Event(2))
		if err != nil {
			t.Fatal(err)
		}
		if first.Sequence != core.FormatSequence(1) || second.Sequence != core.FormatSequence(2) {
			t.Fatalf("sequences = %q, %q", first.Sequence, second.Sequence)
		}
		got, err := repo.After(t.Context(), 0, 10)
		if err != nil || len(got) != 2 {
			t.Fatalf("After() = %d events, %v", len(got), err)
		}
		assertEventEqual(t, got[0], first)
	})
	t.Run("after pages oldest first", func(t *testing.T) {
		repo := newRepo(t)
		for n := 1; n <= 5; n++ {
			if _, err := repo.Append(t.Context(), Event(n)); err != nil {
				t.Fatal(err)
			}
		}
		got, err := repo.After(t.Context(), 2, 2)
		if err != nil || len(got) != 2 || got[0].Sequence != core.FormatSequence(3) || got[1].Sequence != core.FormatSequence(4) {
			t.Fatalf("After(2, 2) = %+v, %v", got, err)
		}
		if last, _ := repo.Last(t.Context()); last != 5 {
			t.Fatalf("Last() = %d, want 5", last)
		}
	})
	t.Run("duplicate event id conflicts", func(t *testing.T) {
		repo := newRepo(t)
		if _, err := repo.Append(t.Context(), Event(1)); err != nil {
			t.Fatal(err)
		}
		if _, err := repo.Append(t.Context(), Event(1)); !errors.Is(err, core.ErrConflict) {
			t.Fatalf("second Append() error = %v, want ErrConflict", err)
		}
	})
	t.Run("event without optional fields round trips", func(t *testing.T) {
		repo := newRepo(t)
		e := Event(1)
		e.Subject, e.DataSchema, e.CausationID = "", "", ""
		stored, err := repo.Append(t.Context(), e)
		if err != nil {
			t.Fatal(err)
		}
		got, _ := repo.After(t.Context(), 0, 1)
		assertEventEqual(t, got[0], stored)
	})
}

func assertEventEqual(t *testing.T, got, want core.Event) {
	t.Helper()
	if got.Time == nil || !got.Time.Equal(*want.Time) {
		t.Errorf("time = %v, want %v", got.Time, want.Time)
	}
	got.Time, want.Time = nil, nil
	if string(got.Data) != string(want.Data) {
		t.Errorf("data = %s, want %s", got.Data, want.Data)
	}
	got.Data, want.Data = nil, nil
	if !reflect.DeepEqual(got, want) {
		t.Errorf("event = %+v, want %+v", got, want)
	}
}

// RunEmitterContract runs the emitter repository contract on fresh repositories.
func RunEmitterContract(t *testing.T, newRepo func(t *testing.T) domain.EmitterRepository) {
	t.Helper()
	ctx := context.Background()
	t.Run("create then get and by name round trip", func(t *testing.T) {
		repo := newRepo(t)
		want := Emitter(1, "disk-watch")
		if err := repo.Create(ctx, want); err != nil {
			t.Fatal(err)
		}
		byID, err := repo.Get(ctx, want.ID)
		if err != nil || !reflect.DeepEqual(byID, want) {
			t.Fatalf("Get() = %+v, %v, want %+v", byID, err, want)
		}
		byName, err := repo.ByName(ctx, "disk-watch")
		if err != nil || byName.ID != want.ID {
			t.Fatalf("ByName() = %+v, %v", byName, err)
		}
	})
	t.Run("unknown emitter is not found", func(t *testing.T) {
		repo := newRepo(t)
		if _, err := repo.Get(ctx, ID("emt", 9)); !errors.Is(err, core.ErrNotFound) {
			t.Errorf("Get() error = %v", err)
		}
		if _, err := repo.ByName(ctx, "nope"); !errors.Is(err, core.ErrNotFound) {
			t.Errorf("ByName() error = %v", err)
		}
		if err := repo.SetEnabled(ctx, ID("emt", 9), false); !errors.Is(err, core.ErrNotFound) {
			t.Errorf("SetEnabled() error = %v", err)
		}
	})
	t.Run("duplicate name is refused", func(t *testing.T) {
		repo := newRepo(t)
		if err := repo.Create(ctx, Emitter(1, "same")); err != nil {
			t.Fatal(err)
		}
		if err := repo.Create(ctx, Emitter(2, "same")); !errors.Is(err, domain.ErrNameTaken) {
			t.Fatalf("Create() error = %v, want ErrNameTaken", err)
		}
	})
	t.Run("list pages in id order", func(t *testing.T) {
		repo := newRepo(t)
		for n := 3; n >= 1; n-- {
			if err := repo.Create(ctx, Emitter(n, "e"+string(rune('a'+n)))); err != nil {
				t.Fatal(err)
			}
		}
		got, err := repo.List(ctx, ID("emt", 1), 5)
		if err != nil || len(got) != 2 || got[0].ID != ID("emt", 2) || got[1].ID != ID("emt", 3) {
			t.Fatalf("List() = %+v, %v", got, err)
		}
		limited, _ := repo.List(ctx, "", 1)
		if len(limited) != 1 || limited[0].ID != ID("emt", 1) {
			t.Fatalf("List(limit 1) = %+v", limited)
		}
	})
	t.Run("set enabled and touch persist", func(t *testing.T) {
		repo := newRepo(t)
		e := Emitter(1, "disk-watch")
		if err := repo.Create(ctx, e); err != nil {
			t.Fatal(err)
		}
		if err := repo.SetEnabled(ctx, e.ID, false); err != nil {
			t.Fatal(err)
		}
		at := At.Add(time.Hour)
		if err := repo.Touch(ctx, "disk-watch", at); err != nil {
			t.Fatal(err)
		}
		got, _ := repo.Get(ctx, e.ID)
		if got.Enabled || got.LastEventAt == nil || !got.LastEventAt.Equal(at) {
			t.Fatalf("emitter = %+v", got)
		}
	})
	t.Run("touch of an unknown name is ignored", func(t *testing.T) {
		if err := newRepo(t).Touch(ctx, "ghost", At); err != nil {
			t.Fatalf("Touch() error = %v", err)
		}
	})
}

// RunTriggerContract runs the trigger repository contract on fresh repositories.
func RunTriggerContract(t *testing.T, newRepo func(t *testing.T) domain.TriggerRepository) {
	t.Helper()
	ctx := context.Background()
	t.Run("create then get round trips", func(t *testing.T) {
		repo := newRepo(t)
		want := Trigger(1, "builder")
		if err := repo.Create(ctx, want); err != nil {
			t.Fatal(err)
		}
		got, err := repo.Get(ctx, want.ID)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("Get() = %+v, %v, want %+v", got, err, want)
		}
	})
	t.Run("unknown trigger is not found", func(t *testing.T) {
		repo := newRepo(t)
		if _, err := repo.Get(ctx, ID("trg", 9)); !errors.Is(err, core.ErrNotFound) {
			t.Errorf("Get() error = %v", err)
		}
		if err := repo.Update(ctx, Trigger(9, "x")); !errors.Is(err, core.ErrNotFound) {
			t.Errorf("Update() error = %v", err)
		}
		if err := repo.Delete(ctx, ID("trg", 9)); !errors.Is(err, core.ErrNotFound) {
			t.Errorf("Delete() error = %v", err)
		}
		if err := repo.RecordFire(ctx, ID("trg", 9), At); !errors.Is(err, core.ErrNotFound) {
			t.Errorf("RecordFire() error = %v", err)
		}
	})
	t.Run("list pages in id order and enabled skips disabled", func(t *testing.T) {
		repo := newRepo(t)
		off := Trigger(2, "builder")
		off.Enabled = false
		for _, tr := range []domain.Trigger{Trigger(3, "builder"), off, Trigger(1, "builder")} {
			if err := repo.Create(ctx, tr); err != nil {
				t.Fatal(err)
			}
		}
		got, err := repo.List(ctx, ID("trg", 1), 5)
		if err != nil || len(got) != 2 || got[0].ID != ID("trg", 2) {
			t.Fatalf("List() = %+v, %v", got, err)
		}
		enabled, err := repo.Enabled(ctx)
		if err != nil || len(enabled) != 2 {
			t.Fatalf("Enabled() = %+v, %v", enabled, err)
		}
	})
	t.Run("update keeps counters", func(t *testing.T) {
		repo := newRepo(t)
		tr := Trigger(1, "builder")
		if err := repo.Create(ctx, tr); err != nil {
			t.Fatal(err)
		}
		if err := repo.RecordFire(ctx, tr.ID, At.Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
		tr.Name, tr.Enabled = "renamed", false
		tr.Action = domain.Action{Kind: domain.ActionCreateTask, Agent: "janitor", Title: "t", Priority: "low"}
		if err := repo.Update(ctx, tr); err != nil {
			t.Fatal(err)
		}
		got, _ := repo.Get(ctx, tr.ID)
		if got.Name != "renamed" || got.Enabled || got.Action.Kind != domain.ActionCreateTask || got.FiredCount != 1 {
			t.Fatalf("trigger = %+v", got)
		}
	})
	t.Run("fire and drop counters", func(t *testing.T) {
		repo := newRepo(t)
		tr := Trigger(1, "builder")
		if err := repo.Create(ctx, tr); err != nil {
			t.Fatal(err)
		}
		when := At.Add(time.Minute)
		for _, f := range []func() error{
			func() error { return repo.RecordFire(ctx, tr.ID, when) },
			func() error { return repo.RecordFire(ctx, tr.ID, when) },
			func() error { return repo.RecordDrop(ctx, tr.ID) },
		} {
			if err := f(); err != nil {
				t.Fatal(err)
			}
		}
		got, _ := repo.Get(ctx, tr.ID)
		if got.FiredCount != 2 || got.DroppedCount != 1 || got.LastFiredAt == nil || !got.LastFiredAt.Equal(when) {
			t.Fatalf("trigger = %+v", got)
		}
	})
	t.Run("delete removes the trigger", func(t *testing.T) {
		repo := newRepo(t)
		tr := Trigger(1, "builder")
		if err := repo.Create(ctx, tr); err != nil {
			t.Fatal(err)
		}
		if err := repo.Delete(ctx, tr.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := repo.Get(ctx, tr.ID); !errors.Is(err, core.ErrNotFound) {
			t.Fatalf("Get() after delete error = %v", err)
		}
	})
}
