// SPDX-License-Identifier: MIT

package sqlite_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/SDK-E/agentsone/internal/sched/adapters/sqlite"
	"github.com/SDK-E/agentsone/internal/sched/domain"
	"github.com/SDK-E/agentsone/internal/sched/schedtest"
	"github.com/SDK-E/agentsone/internal/store"
)

func openMigrated(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "core.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := sqlite.Migrate(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestTaskRepositoryContract(t *testing.T) {
	schedtest.RunTaskRepositoryContract(t, func(t *testing.T) domain.TaskRepository {
		return sqlite.NewTasks(openMigrated(t))
	})
}

func TestScheduleRepositoryContract(t *testing.T) {
	schedtest.RunScheduleRepositoryContract(t, func(t *testing.T) domain.ScheduleRepository {
		return sqlite.NewSchedules(openMigrated(t))
	})
}

func TestMigrateTwiceIsSafe(t *testing.T) {
	db := openMigrated(t)
	if err := sqlite.Migrate(context.Background(), db); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
}

func TestMigrateKeepsDataOfAnEarlierRun(t *testing.T) {
	db := openMigrated(t)
	repo := sqlite.NewTasks(db)
	if err := repo.Insert(context.Background(), schedtest.Task(1)); err != nil {
		t.Fatal(err)
	}
	if err := sqlite.Migrate(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Get(context.Background(), schedtest.ID("tsk", 1)); err != nil {
		t.Fatalf("task lost after migrate: %v", err)
	}
}

func TestRepositoriesReportAClosedDatabase(t *testing.T) {
	db := openMigrated(t)
	tasks, schedules := sqlite.NewTasks(db), sqlite.NewSchedules(db)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := tasks.Get(ctx, "x"); err == nil {
		t.Error("tasks.Get on a closed database returned no error")
	}
	if _, err := tasks.List(ctx, domain.TaskFilter{}); err == nil {
		t.Error("tasks.List on a closed database returned no error")
	}
	if err := tasks.Insert(ctx, schedtest.Task(1)); err == nil {
		t.Error("tasks.Insert on a closed database returned no error")
	}
	if _, err := schedules.Due(ctx, schedtest.Epoch); err == nil {
		t.Error("schedules.Due on a closed database returned no error")
	}
	if err := schedules.Delete(ctx, "x"); err == nil {
		t.Error("schedules.Delete on a closed database returned no error")
	}
}
