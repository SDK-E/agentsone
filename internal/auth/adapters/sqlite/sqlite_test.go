// SPDX-License-Identifier: MIT

package sqlite_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/SDK-E/agentsone/internal/auth/adapters/sqlite"
	"github.com/SDK-E/agentsone/internal/auth/authtest"
	"github.com/SDK-E/agentsone/internal/store"
)

func TestRepository_MeetsTheContract(t *testing.T) {
	t.Parallel()
	authtest.RunRepositoryContract(t, func(t *testing.T) authtest.Repo {
		db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "core.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Close() })
		if err := sqlite.Migrate(context.Background(), db); err != nil {
			t.Fatal(err)
		}
		return sqlite.New(db)
	})
}
