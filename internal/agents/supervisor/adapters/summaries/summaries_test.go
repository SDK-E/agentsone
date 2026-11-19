// SPDX-License-Identifier: MIT

package summaries_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/SDK-E/agentsone/internal/agents/supervisor/adapters/summaries"
	"github.com/SDK-E/agentsone/internal/agents/supervisor/app"
	"github.com/SDK-E/agentsone/internal/store"
)

var _ app.SummaryStore = (*summaries.Store)(nil)

func openStore(t *testing.T) (*summaries.Store, *store.DB) {
	t.Helper()
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "core.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := summaries.Migrate(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	return summaries.New(db), db
}

func TestStore_LoadWithoutSummaryIsTheZeroSummary(t *testing.T) {
	t.Parallel()
	s, _ := openStore(t)
	got, err := s.Load(context.Background(), "mira")
	if err != nil || got != (app.Summary{}) {
		t.Fatalf("Load = %+v, %v; want the zero summary", got, err)
	}
}

// A saved summary comes back after the daemon restarts, and a newer one
// replaces it.
func TestStore_SavedSummarySurvivesARestartAndIsReplaced(t *testing.T) {
	t.Parallel()
	s, db := openStore(t)
	at := time.Date(2026, 11, 20, 1, 2, 3, 400, time.UTC)
	first := app.Summary{Text: "Chatted with alice.", CompactedAt: at, PausedTurnID: "trn_01"}
	if err := s.Save(context.Background(), "mira", first); err != nil {
		t.Fatal(err)
	}
	if err := summaries.Migrate(context.Background(), db); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}
	again := summaries.New(db)
	got, err := again.Load(context.Background(), "mira")
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(first, got); diff != "" {
		t.Fatalf("Load (-want +got):\n%s", diff)
	}

	second := app.Summary{Text: "Reviewed the plan.", CompactedAt: at.Add(time.Hour)}
	if err := again.Save(context.Background(), "mira", second); err != nil {
		t.Fatal(err)
	}
	if got, _ := again.Load(context.Background(), "mira"); got != second {
		t.Fatalf("Load after the second save = %+v, want %+v", got, second)
	}
	if got, _ := again.Load(context.Background(), "dev"); got != (app.Summary{}) {
		t.Fatalf("another agent's summary = %+v, want none", got)
	}
}

func TestStore_ReportsAClosedDatabase(t *testing.T) {
	t.Parallel()
	s, db := openStore(t)
	_ = db.Close()
	if _, err := s.Load(context.Background(), "mira"); err == nil {
		t.Error("Load on a closed database: no error")
	}
	if err := s.Save(context.Background(), "mira", app.Summary{Text: "x"}); err == nil {
		t.Error("Save on a closed database: no error")
	}
	if err := summaries.Migrate(context.Background(), db); err == nil || errors.Is(err, context.Canceled) {
		t.Errorf("Migrate on a closed database = %v, want an error", err)
	}
}
