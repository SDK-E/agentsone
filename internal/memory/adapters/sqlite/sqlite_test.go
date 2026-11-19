// SPDX-License-Identifier: MIT

package sqlite_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SDK-E/agentsone/internal/memory/adapters/sqlite"
	"github.com/SDK-E/agentsone/internal/memory/domain"
	"github.com/SDK-E/agentsone/internal/memory/memorytest"
	"github.com/SDK-E/agentsone/internal/store"
)

func openMigrated(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "memory.db"))
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
	memorytest.RunRepositoryContract(t, func(t *testing.T) domain.Repository {
		return sqlite.New(openMigrated(t))
	})
}

func TestMigrate_TwiceIsSafe(t *testing.T) {
	db := openMigrated(t)
	if err := sqlite.Migrate(context.Background(), db); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
}

func TestSave_RefusesTheSameKeyOnAnotherRecordOfTheOwner(t *testing.T) {
	repo := sqlite.New(openMigrated(t))
	a := memorytest.Rec(1, domain.Common, "scribe", "builder", "A", "")
	a.Key = "notes"
	b := memorytest.Rec(2, domain.Common, "scribe", "builder", "B", "")
	b.Key = "notes"
	if err := repo.Save(context.Background(), a); err != nil {
		t.Fatal(err)
	}

	if err := repo.Save(context.Background(), b); err == nil {
		t.Fatal("two records of one owner share a key")
	}
}

func TestSearch_RanksByWeightedBM25(t *testing.T) {
	repo := sqlite.New(openMigrated(t))
	ctx := context.Background()
	for _, r := range []domain.Record{
		memorytest.Rec(1, domain.Common, "a", "a", "Weekly report", strings.Repeat("disk ", 3)),
		memorytest.Rec(2, domain.Common, "a", "a", "Disk", "short"),
	} {
		if err := repo.Save(ctx, r); err != nil {
			t.Fatal(err)
		}
	}

	page, err := repo.Search(ctx, domain.SearchQuery{Visibility: domain.Visibility{All: true}, Text: "disk", Limit: 10})

	if err != nil || len(page.Items) != 2 || page.Items[0].Record.ID != "mem_02" {
		t.Fatalf("page %+v, err %v", page, err)
	}
}

func TestSearch_CutsALongSnippetToFiveHundredCharacters(t *testing.T) {
	repo := sqlite.New(openMigrated(t))
	ctx := context.Background()
	long := strings.Repeat("extraordinarilylongwordsmakeasnippetoverflow ", 40) + " journal"
	if err := repo.Save(ctx, memorytest.Rec(1, domain.Common, "a", "a", "T", long)); err != nil {
		t.Fatal(err)
	}

	page, err := repo.Search(ctx, domain.SearchQuery{Visibility: domain.Visibility{All: true}, Text: "journal extraordinarilylongwordsmakeasnippetoverflow", Limit: 10})

	if err != nil || len(page.Items) != 1 || len([]rune(page.Items[0].Snippet)) > 500 {
		t.Fatalf("page %+v, err %v", page, err)
	}
}

func TestOperationsFailAfterTheDatabaseClosed(t *testing.T) {
	db := openMigrated(t)
	repo := sqlite.New(db)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	r := memorytest.Rec(1, domain.Common, "a", "a", "T", "b")
	v := domain.Visibility{All: true}

	checks := map[string]error{
		"save":   repo.Save(ctx, r),
		"delete": repo.Delete(ctx, "mem_01"),
	}
	_, checks["get"] = repo.Get(ctx, "mem_01")
	_, _, checks["find"] = repo.FindByKey(ctx, "a", domain.Common, "k")
	_, checks["list"] = repo.List(ctx, domain.ListQuery{Visibility: v, Limit: 1})
	_, checks["search"] = repo.Search(ctx, domain.SearchQuery{Visibility: v, Text: "t", Limit: 1})
	for name, err := range checks {
		if err == nil {
			t.Errorf("%s: no error on a closed database", name)
		}
		if errors.Is(err, domain.ErrNotFound) {
			t.Errorf("%s: a closed database reads as not found", name)
		}
	}
}
