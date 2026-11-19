// SPDX-License-Identifier: MIT

package sqlite_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/SDK-E/agentsone/internal/events/adapters/sqlite"
	"github.com/SDK-E/agentsone/internal/events/domain"
	"github.com/SDK-E/agentsone/internal/events/eventstest"
	"github.com/SDK-E/agentsone/internal/store"
)

func openMigrated(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "bus.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := sqlite.Migrate(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestEventsRepository(t *testing.T) {
	eventstest.RunEventContract(t, func(t *testing.T) domain.EventRepository { return sqlite.NewEvents(openMigrated(t)) })
}

func TestEmittersRepository(t *testing.T) {
	eventstest.RunEmitterContract(t, func(t *testing.T) domain.EmitterRepository { return sqlite.NewEmitters(openMigrated(t)) })
}

func TestTriggersRepository(t *testing.T) {
	eventstest.RunTriggerContract(t, func(t *testing.T) domain.TriggerRepository { return sqlite.NewTriggers(openMigrated(t)) })
}

func TestMigrateTwiceIsSafe(t *testing.T) {
	db := openMigrated(t)
	if err := sqlite.Migrate(context.Background(), db); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
}

func TestSequencesSurviveReopening(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bus.db")
	open := func() *sqlite.Events {
		db, err := store.Open(context.Background(), path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Close() })
		if err := sqlite.Migrate(context.Background(), db); err != nil {
			t.Fatal(err)
		}
		return sqlite.NewEvents(db)
	}
	if _, err := open().Append(context.Background(), eventstest.Event(1)); err != nil {
		t.Fatal(err)
	}

	stored, err := open().Append(context.Background(), eventstest.Event(2))

	if err != nil || stored.Sequence != "00000000000000000002" {
		t.Fatalf("Append() after reopen = %q, %v", stored.Sequence, err)
	}
}
