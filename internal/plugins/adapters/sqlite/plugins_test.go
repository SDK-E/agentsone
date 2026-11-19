// SPDX-License-Identifier: MIT

package sqlite_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/SDK-E/agentsone/internal/plugins/adapters/sqlite"
	"github.com/SDK-E/agentsone/internal/plugins/domain"
	"github.com/SDK-E/agentsone/internal/plugins/pluginstest"
	"github.com/SDK-E/agentsone/internal/store"
)

// openMigrated opens a database and runs the module's own Migrate.
func openMigrated(t *testing.T) *store.DB {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "plugins.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := sqlite.Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	return db
}

func TestPluginsContract(t *testing.T) {
	t.Parallel()
	pluginstest.RunPluginRepositoryContract(t, func(t *testing.T) domain.PluginRepository {
		return sqlite.NewPlugins(openMigrated(t))
	})
}

func TestMigrate_CreatesPluginAndThemeTablesAndIsRepeatable(t *testing.T) {
	t.Parallel()
	db := openMigrated(t)

	if err := sqlite.Migrate(context.Background(), db); err != nil {
		t.Fatalf("second Migrate() error = %v", err)
	}

	for _, table := range []string{"plugins", "themes", "theme_selection"} {
		var n int
		err := db.SQL().QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&n)
		if err != nil || n != 1 {
			t.Errorf("table %s: count = %d, err = %v", table, n, err)
		}
	}
}

func TestPlugins_CorruptRowsAreErrors(t *testing.T) {
	t.Parallel()
	db := openMigrated(t)
	repo := sqlite.NewPlugins(db)
	good := pluginstest.Plugin(1)
	if err := repo.Insert(context.Background(), good); err != nil {
		t.Fatal(err)
	}
	for name, update := range map[string]string{
		"installed_at": `UPDATE plugins SET installed_at = 'yesterday'`,
		"manifest":     `UPDATE plugins SET manifest = '{"name":'`,
		"kinds":        `UPDATE plugins SET kinds = 'not json'`,
	} {
		t.Run(name, func(t *testing.T) {
			bad := openMigrated(t)
			badRepo := sqlite.NewPlugins(bad)
			if err := badRepo.Insert(context.Background(), good); err != nil {
				t.Fatal(err)
			}
			if _, err := bad.SQL().Exec(update); err != nil {
				t.Fatal(err)
			}
			if _, err := badRepo.Get(context.Background(), good.ID); err == nil {
				t.Error("Get() accepted a corrupt row")
			}
			if _, err := badRepo.List(context.Background(), domain.PluginFilter{}, "", 10); err == nil {
				t.Error("List() accepted a corrupt row")
			}
			if _, err := badRepo.ByName(context.Background(), good.Name); err == nil {
				t.Error("ByName() accepted a corrupt row")
			}
		})
	}
}
