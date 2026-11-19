// SPDX-License-Identifier: MIT

package sqlite_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	budgetstore "github.com/SDK-E/agentsone/internal/edge/budget/adapters/sqlite"
	budgetapp "github.com/SDK-E/agentsone/internal/edge/budget/app"
	budget "github.com/SDK-E/agentsone/internal/edge/budget/domain"
)

func openStore(t *testing.T, path string) *budgetstore.Store {
	t.Helper()
	s, err := budgetstore.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return s
}

func TestFreshStoreIsEmpty(t *testing.T) {
	s := openStore(t, filepath.Join(t.TempDir(), "budget.db"))
	ctx := context.Background()
	set, err := s.LoadSettings(ctx)
	if err != nil || set != (budgetapp.Settings{Period: budget.PeriodMonth}) {
		t.Fatalf("LoadSettings = %+v, %v", set, err)
	}
	sp, err := s.LoadSpend(ctx)
	if err != nil || sp.General != 0 || sp.Defence != 0 || len(sp.Agents) != 0 {
		t.Fatalf("LoadSpend = %+v, %v", sp, err)
	}
}

func TestSettingsAndSpendSurviveReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "budget.db")
	ctx := context.Background()

	first, err := budgetstore.Open(ctx, path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	wantSet := budgetapp.Settings{Total: 42 * budget.MicrosPerUSD, DefenceOn: true, DefenceSharePct: 15, Period: budget.PeriodWeek}
	if err := first.SaveSettings(ctx, wantSet); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}
	if err := first.SaveSpend(ctx, budgetapp.Spend{General: 5, Defence: 7, Agents: map[string]budget.Micros{"a": 5, "b": 7}}); err != nil {
		t.Fatalf("SaveSpend: %v", err)
	}
	// A second save replaces the first, agents dropped from the map included.
	periodStart := time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC)
	if err := first.SaveSpend(ctx, budgetapp.Spend{General: 9, Defence: 7, Agents: map[string]budget.Micros{"a": 9}, PeriodStart: periodStart}); err != nil {
		t.Fatalf("SaveSpend: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	again := openStore(t, path)
	gotSet, err := again.LoadSettings(ctx)
	if err != nil || gotSet != wantSet {
		t.Fatalf("LoadSettings = %+v, %v; want %+v", gotSet, err, wantSet)
	}
	sp, err := again.LoadSpend(ctx)
	if err != nil {
		t.Fatalf("LoadSpend: %v", err)
	}
	if sp.General != 9 || sp.Defence != 7 || len(sp.Agents) != 1 || sp.Agents["a"] != 9 || !sp.PeriodStart.Equal(periodStart) {
		t.Fatalf("LoadSpend = %+v", sp)
	}
}

func TestOpenUpgradesADatabaseFromBeforePeriods(t *testing.T) {
	path := filepath.Join(t.TempDir(), "budget.db")
	ctx := context.Background()
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	for _, stmt := range []string{
		`CREATE TABLE settings (id INTEGER PRIMARY KEY CHECK (id = 1), total_micros INTEGER NOT NULL,
			defence_on INTEGER NOT NULL, defence_share_pct INTEGER NOT NULL)`,
		`CREATE TABLE spend (id INTEGER PRIMARY KEY CHECK (id = 1), general_micros INTEGER NOT NULL, defence_micros INTEGER NOT NULL)`,
		`CREATE TABLE agent_spend (agent TEXT PRIMARY KEY, micros INTEGER NOT NULL)`,
		`INSERT INTO settings VALUES (1, 7000000, 0, 0)`,
		`INSERT INTO spend VALUES (1, 2000000, 0)`,
	} {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	_ = db.Close()

	s := openStore(t, path)

	set, err := s.LoadSettings(ctx)
	if err != nil || set.Total != 7*budget.MicrosPerUSD || set.Period != budget.PeriodMonth {
		t.Fatalf("LoadSettings = %+v, %v; want the old total and a month", set, err)
	}
	sp, err := s.LoadSpend(ctx)
	if err != nil || sp.General != 2*budget.MicrosPerUSD || !sp.PeriodStart.IsZero() {
		t.Fatalf("LoadSpend = %+v, %v; want the old spending with no period yet", sp, err)
	}
	// Opening again finds the database already upgraded.
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	openStore(t, path)
}

func TestTwoHandlesShareOneFile(t *testing.T) {
	// The gateway and the `agentsone budget` command open the same file.
	path := filepath.Join(t.TempDir(), "budget.db")
	ctx := context.Background()
	gw := openStore(t, path)
	cli := openStore(t, path)
	if err := cli.SaveSettings(ctx, budgetapp.Settings{Total: 3}); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}
	got, err := gw.LoadSettings(ctx)
	if err != nil || got.Total != 3 {
		t.Fatalf("LoadSettings = %+v, %v", got, err)
	}
}

func TestOpenFailsOnBadPath(t *testing.T) {
	if _, err := budgetstore.Open(context.Background(), filepath.Join(t.TempDir(), "missing", "dir", "budget.db")); err == nil {
		t.Fatal("Open succeeded in a missing directory")
	}
}

func TestClosedStoreReportsErrors(t *testing.T) {
	s, err := budgetstore.Open(context.Background(), filepath.Join(t.TempDir(), "budget.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	ctx := context.Background()
	if _, err := s.LoadSettings(ctx); err == nil {
		t.Fatal("LoadSettings on closed store succeeded")
	}
	if err := s.SaveSettings(ctx, budgetapp.Settings{}); err == nil {
		t.Fatal("SaveSettings on closed store succeeded")
	}
	if _, err := s.LoadSpend(ctx); err == nil {
		t.Fatal("LoadSpend on closed store succeeded")
	}
	if err := s.SaveSpend(ctx, budgetapp.Spend{}); err == nil {
		t.Fatal("SaveSpend on closed store succeeded")
	}
}
