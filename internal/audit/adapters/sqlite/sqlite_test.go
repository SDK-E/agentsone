// SPDX-License-Identifier: MIT

package sqlite_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/SDK-E/agentsone/internal/audit/adapters/sqlite"
	"github.com/SDK-E/agentsone/internal/audit/audittest"
	"github.com/SDK-E/agentsone/internal/audit/domain"
	"github.com/SDK-E/agentsone/internal/store"
)

func openMigrated(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "audit.db"))
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
	audittest.RunRepositoryContract(t, func(t *testing.T) domain.Repository {
		return sqlite.New(openMigrated(t))
	})
}

func TestMigrateTwiceIsSafe(t *testing.T) {
	db := openMigrated(t)
	if err := sqlite.Migrate(context.Background(), db); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
}

func TestRecordWithoutUIDRoundTrips(t *testing.T) {
	repo := sqlite.New(openMigrated(t))
	r := audittest.Chain(1)[0]
	r.Actor.UID = nil
	if err := repo.Append(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	got, _, err := repo.Last(context.Background())
	if err != nil || got.Actor.UID != nil {
		t.Fatalf("uid %v, err %v", got.Actor.UID, err)
	}
}
