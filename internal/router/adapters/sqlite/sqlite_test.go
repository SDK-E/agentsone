// SPDX-License-Identifier: MIT

package sqlite_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/SDK-E/agentsone/internal/router/adapters/sqlite"
	"github.com/SDK-E/agentsone/internal/router/domain"
	"github.com/SDK-E/agentsone/internal/router/routertest"
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

func TestLinesContract(t *testing.T) {
	routertest.RunLineContract(t, func(t *testing.T) domain.LineRepository { return sqlite.New(openMigrated(t)) })
}

func TestAssignmentsContract(t *testing.T) {
	routertest.RunAssignmentContract(t, func(t *testing.T) domain.AssignmentRepository { return sqlite.New(openMigrated(t)) })
}

func TestMigrateTwiceIsSafe(t *testing.T) {
	db := openMigrated(t)
	if err := sqlite.Migrate(context.Background(), db); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
}

func TestSaveRejectsADuplicateAssignment(t *testing.T) {
	repo := sqlite.New(openMigrated(t))
	a := domain.Assignment{ID: "asg_1", Text: "t", Chosen: "b", Router: "r", ChatPath: "/p", ChatMessageID: "cht_1", CreatedBy: "human:ada"}
	if err := repo.Save(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	if err := repo.Save(context.Background(), a); err == nil {
		t.Fatal("duplicate id saved twice")
	}
}

func TestClosedDatabaseSurfacesErrors(t *testing.T) {
	db := openMigrated(t)
	repo := sqlite.New(db)
	_ = db.Close()
	ctx := context.Background()
	if _, err := repo.List(ctx); err == nil {
		t.Error("List on a closed database did not fail")
	}
	if err := repo.Delete(ctx, "x"); err == nil {
		t.Error("Delete on a closed database did not fail")
	}
	if _, err := repo.Get(ctx, "x"); err == nil {
		t.Error("Get on a closed database did not fail")
	}
}
