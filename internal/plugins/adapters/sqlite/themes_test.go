// SPDX-License-Identifier: MIT

package sqlite_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/SDK-E/agentsone/internal/plugins/adapters/sqlite"
	"github.com/SDK-E/agentsone/internal/plugins/domain"
	"github.com/SDK-E/agentsone/internal/plugins/pluginstest"
	"github.com/SDK-E/agentsone/internal/store"
)

// openThemes opens a database and applies the migrations on disk, so this
// test does not depend on how the module embeds them.
func openThemes(t *testing.T) (*store.DB, *sqlite.Themes) {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "plugins.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ms, err := store.MigrationsFromFS(os.DirFS("."), "migrations")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(ctx, "plugins-test", ms); err != nil {
		t.Fatal(err)
	}
	return db, sqlite.NewThemes(db)
}

func TestThemesContract(t *testing.T) {
	t.Parallel()
	pluginstest.RunThemeRepositoryContract(t, func(t *testing.T) domain.ThemeRepository {
		_, repo := openThemes(t)
		return repo
	})
}

func TestThemes_CorruptTimestampIsAnError(t *testing.T) {
	t.Parallel()
	db, repo := openThemes(t)
	_, err := db.SQL().Exec(`INSERT INTO themes (id, name, version, installed_at, document) VALUES ('thm_x', 'X', '1.0.0', 'yesterday', '{}')`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Get(context.Background(), "thm_x"); err == nil {
		t.Fatal("Get() accepted a corrupt installed_at")
	}
	if _, err := repo.List(context.Background(), "", 10); err == nil {
		t.Fatal("List() accepted a corrupt installed_at")
	}
}

func TestThemes_DatabaseRefusesDeletingASelectedThemeEvenBehindTheRepositoryBack(t *testing.T) {
	t.Parallel()
	db, repo := openThemes(t)
	ctx := context.Background()
	if err := repo.Insert(ctx, pluginstest.Theme(1)); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetSelection(ctx, "ada", domain.Selection{ThemeID: pluginstest.Theme(1).ID, Mode: domain.ModeDark}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SQL().Exec(`DELETE FROM themes WHERE id = ?`, pluginstest.Theme(1).ID); err == nil {
		t.Fatal("foreign key did not stop the delete")
	}
}

func TestThemes_ClosedDatabaseSurfacesErrors(t *testing.T) {
	t.Parallel()
	db, repo := openThemes(t)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := repo.Insert(ctx, pluginstest.Theme(1)); err == nil {
		t.Error("Insert() on a closed database succeeded")
	}
	if err := repo.Update(ctx, pluginstest.Theme(1)); err == nil {
		t.Error("Update() on a closed database succeeded")
	}
	if _, err := repo.Get(ctx, "x"); err == nil {
		t.Error("Get() on a closed database succeeded")
	}
	if _, _, err := repo.FindByName(ctx, "x"); err == nil {
		t.Error("FindByName() on a closed database succeeded")
	}
	if _, err := repo.List(ctx, "", 1); err == nil {
		t.Error("List() on a closed database succeeded")
	}
	if err := repo.Delete(ctx, "x"); err == nil {
		t.Error("Delete() on a closed database succeeded")
	}
	if _, _, err := repo.Selection(ctx, "ada"); err == nil {
		t.Error("Selection() on a closed database succeeded")
	}
	if err := repo.SetSelection(ctx, "ada", domain.Selection{}); err == nil {
		t.Error("SetSelection() on a closed database succeeded")
	}
	if err := repo.ReassignSelections(ctx, "a", "b"); err == nil {
		t.Error("ReassignSelections() on a closed database succeeded")
	}
}
