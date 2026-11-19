// SPDX-License-Identifier: MIT

package sqlite_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/SDK-E/agentsone/internal/agents/adapters/sqlite"
	"github.com/SDK-E/agentsone/internal/agents/agentstest"
	"github.com/SDK-E/agentsone/internal/agents/domain"
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

func TestRepositoryContract(t *testing.T) {
	t.Parallel()
	agentstest.RunRepositoryContract(t, func(t *testing.T) domain.Repository {
		return sqlite.New(openMigrated(t))
	})
}

func TestMigrate_TwiceIsSafe(t *testing.T) {
	t.Parallel()
	db := openMigrated(t)
	if err := sqlite.Migrate(context.Background(), db); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
}

func TestRepository_ReportsAClosedDatabase(t *testing.T) {
	t.Parallel()
	db := openMigrated(t)
	repo := sqlite.New(db)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	one := int64(1)
	calls := map[string]func() error{
		"ListAgents":    func() error { _, err := repo.ListAgents(ctx); return err },
		"GetAgent":      func() error { _, err := repo.GetAgent(ctx, "dev"); return err },
		"CreateAgent":   func() error { return repo.CreateAgent(ctx, agentstest.New("dev")) },
		"SaveAgents":    func() error { return repo.SaveAgents(ctx, agentstest.New("dev")) },
		"Charter":       func() error { _, err := repo.Charter(ctx); return err },
		"SaveCharter":   func() error { return repo.SaveCharter(ctx, domain.Charter{Version: 1}) },
		"CreateGoal":    func() error { return repo.CreateGoal(ctx, domain.Goal{ID: "gol_1"}) },
		"GetGoal":       func() error { _, err := repo.GetGoal(ctx, "gol_1"); return err },
		"SaveGoal":      func() error { return repo.SaveGoal(ctx, domain.Goal{ID: "gol_1"}) },
		"DeleteGoal":    func() error { return repo.DeleteGoal(ctx, "gol_1") },
		"ListGoals":     func() error { _, err := repo.ListGoals(ctx, domain.GoalFilter{}); return err },
		"Pool":          func() error { _, err := repo.Pool(ctx); return err },
		"SavePool":      func() error { return repo.SavePool(ctx, domain.Pool{}) },
		"Allocations":   func() error { _, err := repo.Allocations(ctx); return err },
		"SetAllocation": func() error { return repo.SetAllocation(ctx, "dev", &one) },
		"Usage":         func() error { _, err := repo.Usage(ctx); return err },
		"AddUsage":      func() error { return repo.AddUsage(ctx, "dev", 1) },
		"AppendChat":    func() error { return repo.AppendChat(ctx, domain.ChatMessage{ID: "cht_1"}) },
		"ListChat":      func() error { _, err := repo.ListChat(ctx, "dev", "", 1); return err },
	}
	for name, call := range calls {
		if err := call(); err == nil {
			t.Errorf("%s on a closed database returned no error", name)
		}
	}
}
