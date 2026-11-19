// SPDX-License-Identifier: MIT

// Package pluginstest holds contract suites that every plugins repository
// must pass, so the memory fake and the sqlite adapter cannot drift apart.
package pluginstest

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/plugins/domain"
)

var themeEpoch = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

// Theme returns an installed theme with a stable id built from n.
func Theme(n int) domain.InstalledTheme {
	return domain.InstalledTheme{
		ID:          fmt.Sprintf("thm_%026d", n),
		Name:        fmt.Sprintf("Theme %d", n),
		Version:     "1.0.0",
		InstalledAt: themeEpoch.Add(time.Duration(n) * time.Minute),
		Document:    []byte(fmt.Sprintf(`{"name":"Theme %d"}`, n)),
	}
}

// RunThemeRepositoryContract runs the contract on a fresh repository from
// newRepo for every case.
func RunThemeRepositoryContract(t *testing.T, newRepo func(t *testing.T) domain.ThemeRepository) {
	t.Helper()
	cases := map[string]func(t *testing.T, repo domain.ThemeRepository){
		"insert then get round-trips every field":     insertRoundTrip,
		"get of a missing theme is not found":         getMissing,
		"insert refuses a duplicate name":             insertDuplicateName,
		"find by name":                                findByName,
		"list pages by id":                            listPages,
		"by plugin returns only that plugin's themes": byPlugin,
		"builtin returns the built-in theme":          builtin,
		"update replaces version and document":        update,
		"update of a missing theme is not found":      updateMissing,
		"delete removes the theme":                    deleteTheme,
		"delete of a missing theme is not found":      deleteMissing,
		"delete refuses a theme somebody selected":    deleteInUse,
		"selection round-trips and can change":        selectionRoundTrip,
		"selection of a missing theme is not found":   selectionMissingTheme,
		"selection is none before a choice":           selectionNone,
		"reassign moves every selection and frees it": reassign,
	}
	for name, run := range cases {
		t.Run(name, func(t *testing.T) {
			run(t, newRepo(t))
		})
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func insert(t *testing.T, repo domain.ThemeRepository, th domain.InstalledTheme) {
	t.Helper()
	must(t, repo.Insert(context.Background(), th))
}

func insertRoundTrip(t *testing.T, repo domain.ThemeRepository) {
	want := Theme(1)
	want.Builtin = true
	want.PluginID = "plg_01JD3Q8Z5X6Y7W8V9T0S1R2Q3P"
	insert(t, repo, want)

	got, err := repo.Get(context.Background(), want.ID)
	must(t, err)

	if got.ID != want.ID || got.Name != want.Name || got.Version != want.Version ||
		got.Builtin != want.Builtin || got.PluginID != want.PluginID ||
		!got.InstalledAt.Equal(want.InstalledAt) || string(got.Document) != string(want.Document) {
		t.Fatalf("Get() = %+v, want %+v", got, want)
	}
}

func getMissing(t *testing.T, repo domain.ThemeRepository) {
	if _, err := repo.Get(context.Background(), "thm_none"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Get() error = %v, want ErrNotFound", err)
	}
}

func insertDuplicateName(t *testing.T, repo domain.ThemeRepository) {
	insert(t, repo, Theme(1))
	again := Theme(2)
	again.Name = Theme(1).Name
	if err := repo.Insert(context.Background(), again); !errors.Is(err, domain.ErrAlreadyInstalled) {
		t.Fatalf("Insert() error = %v, want ErrAlreadyInstalled", err)
	}
}

func findByName(t *testing.T, repo domain.ThemeRepository) {
	insert(t, repo, Theme(1))
	got, ok, err := repo.FindByName(context.Background(), Theme(1).Name)
	must(t, err)
	if !ok || got.ID != Theme(1).ID {
		t.Fatalf("FindByName() = %+v, %v", got, ok)
	}
	if _, ok, err := repo.FindByName(context.Background(), "nope"); err != nil || ok {
		t.Fatalf("FindByName(missing) = %v, %v", ok, err)
	}
}

func listPages(t *testing.T, repo domain.ThemeRepository) {
	for n := 1; n <= 5; n++ {
		insert(t, repo, Theme(n))
	}
	ctx := context.Background()

	first, err := repo.List(ctx, "", 2)
	must(t, err)
	if len(first) != 2 || first[0].ID != Theme(1).ID || first[1].ID != Theme(2).ID {
		t.Fatalf("first page = %+v", first)
	}
	next, err := repo.List(ctx, first[1].ID, 10)
	must(t, err)
	if len(next) != 3 || next[0].ID != Theme(3).ID || next[2].ID != Theme(5).ID {
		t.Fatalf("next page = %+v", next)
	}
	end, err := repo.List(ctx, Theme(5).ID, 10)
	must(t, err)
	if len(end) != 0 {
		t.Fatalf("page after the last = %+v", end)
	}
}

func byPlugin(t *testing.T, repo domain.ThemeRepository) {
	a, b := Theme(1), Theme(2)
	a.PluginID, b.PluginID = "plg_A", "plg_B"
	insert(t, repo, a)
	insert(t, repo, b)
	insert(t, repo, Theme(3))

	got, err := repo.ByPlugin(context.Background(), "plg_A")
	must(t, err)
	if len(got) != 1 || got[0].ID != a.ID {
		t.Fatalf("ByPlugin() = %+v", got)
	}
}

func builtin(t *testing.T, repo domain.ThemeRepository) {
	ctx := context.Background()
	if _, ok, err := repo.Builtin(ctx); err != nil || ok {
		t.Fatalf("Builtin() before seed = %v, %v", ok, err)
	}
	insert(t, repo, Theme(1))
	b := Theme(2)
	b.Builtin = true
	insert(t, repo, b)
	got, ok, err := repo.Builtin(ctx)
	must(t, err)
	if !ok || got.ID != b.ID {
		t.Fatalf("Builtin() = %+v, %v", got, ok)
	}
}

func update(t *testing.T, repo domain.ThemeRepository) {
	th := Theme(1)
	insert(t, repo, th)
	th.Version = "2.0.0"
	th.Document = []byte(`{"name":"Theme 1","v":2}`)
	must(t, repo.Update(context.Background(), th))

	got, err := repo.Get(context.Background(), th.ID)
	must(t, err)
	if got.Version != "2.0.0" || string(got.Document) != string(th.Document) {
		t.Fatalf("Get() after update = %+v", got)
	}
}

func updateMissing(t *testing.T, repo domain.ThemeRepository) {
	if err := repo.Update(context.Background(), Theme(1)); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Update() error = %v, want ErrNotFound", err)
	}
}

func deleteTheme(t *testing.T, repo domain.ThemeRepository) {
	insert(t, repo, Theme(1))
	must(t, repo.Delete(context.Background(), Theme(1).ID))
	if _, err := repo.Get(context.Background(), Theme(1).ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Get() after delete error = %v", err)
	}
}

func deleteMissing(t *testing.T, repo domain.ThemeRepository) {
	if err := repo.Delete(context.Background(), "thm_none"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Delete() error = %v, want ErrNotFound", err)
	}
}

func deleteInUse(t *testing.T, repo domain.ThemeRepository) {
	insert(t, repo, Theme(1))
	must(t, repo.SetSelection(context.Background(), "ada", domain.Selection{ThemeID: Theme(1).ID, Mode: domain.ModeDark}))

	if err := repo.Delete(context.Background(), Theme(1).ID); !errors.Is(err, domain.ErrInUse) {
		t.Fatalf("Delete() error = %v, want ErrInUse", err)
	}
	if _, err := repo.Get(context.Background(), Theme(1).ID); err != nil {
		t.Fatalf("theme vanished: %v", err)
	}
}

func selectionRoundTrip(t *testing.T, repo domain.ThemeRepository) {
	ctx := context.Background()
	insert(t, repo, Theme(1))
	insert(t, repo, Theme(2))
	must(t, repo.SetSelection(ctx, "ada", domain.Selection{ThemeID: Theme(1).ID, Mode: domain.ModeLight}))
	must(t, repo.SetSelection(ctx, "bob", domain.Selection{ThemeID: Theme(2).ID, Mode: domain.ModeSystem}))
	must(t, repo.SetSelection(ctx, "ada", domain.Selection{ThemeID: Theme(2).ID, Mode: domain.ModeDark}))

	got, ok, err := repo.Selection(ctx, "ada")
	must(t, err)
	if !ok || got.ThemeID != Theme(2).ID || got.Mode != domain.ModeDark {
		t.Fatalf("Selection(ada) = %+v, %v", got, ok)
	}
	got, _, err = repo.Selection(ctx, "bob")
	must(t, err)
	if got.Mode != domain.ModeSystem {
		t.Fatalf("Selection(bob) = %+v", got)
	}
}

func selectionMissingTheme(t *testing.T, repo domain.ThemeRepository) {
	err := repo.SetSelection(context.Background(), "ada", domain.Selection{ThemeID: "thm_none", Mode: domain.ModeDark})
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("SetSelection() error = %v, want ErrNotFound", err)
	}
}

func selectionNone(t *testing.T, repo domain.ThemeRepository) {
	if _, ok, err := repo.Selection(context.Background(), "ada"); err != nil || ok {
		t.Fatalf("Selection() = %v, %v", ok, err)
	}
}

func reassign(t *testing.T, repo domain.ThemeRepository) {
	ctx := context.Background()
	insert(t, repo, Theme(1))
	insert(t, repo, Theme(2))
	must(t, repo.SetSelection(ctx, "ada", domain.Selection{ThemeID: Theme(1).ID, Mode: domain.ModeDark}))
	must(t, repo.SetSelection(ctx, "bob", domain.Selection{ThemeID: Theme(1).ID, Mode: domain.ModeLight}))

	must(t, repo.ReassignSelections(ctx, Theme(1).ID, Theme(2).ID))

	got, _, err := repo.Selection(ctx, "ada")
	must(t, err)
	if got.ThemeID != Theme(2).ID || got.Mode != domain.ModeDark {
		t.Fatalf("Selection(ada) = %+v, want theme 2 with the mode kept", got)
	}
	must(t, repo.Delete(ctx, Theme(1).ID))
}
