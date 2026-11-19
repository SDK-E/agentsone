// SPDX-License-Identifier: MIT

package app_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/core/coretest"
	"github.com/SDK-E/agentsone/internal/plugins/adapters/memory"
	"github.com/SDK-E/agentsone/internal/plugins/app"
	"github.com/SDK-E/agentsone/internal/plugins/domain"
)

const draftTheme = "../../../contract/fixtures/schemas/theme/valid/agents-one-theme-draft.json"

type themeEnv struct {
	themes *app.Themes
	repo   *memory.Themes
	audit  *coretest.Audit
	clock  *coretest.Clock
}

func newThemeEnv(t *testing.T) themeEnv {
	t.Helper()
	repo := memory.NewThemes()
	audit := coretest.NewAudit()
	clock := coretest.NewClock(coretest.Epoch)
	return themeEnv{
		themes: app.NewThemes(repo, audit, clock, coretest.NewIDs()),
		repo:   repo, audit: audit, clock: clock,
	}
}

func draftDocument(t *testing.T, change func(doc map[string]any)) []byte {
	t.Helper()
	raw, err := os.ReadFile(draftTheme)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if change != nil {
		change(doc)
	}
	out, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func named(name, version string) func(map[string]any) {
	return func(d map[string]any) { d["name"], d["version"] = name, version }
}

func human(name string) context.Context {
	return core.WithCaller(context.Background(), core.Caller{Kind: core.CallerHuman, Name: name, UID: -1})
}

func seedBuiltin(t *testing.T, env themeEnv) domain.InstalledTheme {
	t.Helper()
	b, err := env.themes.EnsureBuiltin(context.Background(), draftDocument(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestThemes_ImportStoresTheThemeAndAuditsIt(t *testing.T) {
	t.Parallel()
	env := newThemeEnv(t)
	ctx := human("ada")

	got, err := env.themes.Import(ctx, draftDocument(t, named("Ocean", "1.2.0")))
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}

	if !strings.HasPrefix(got.ID, "thm_") || got.Name != "Ocean" || got.Version != "1.2.0" || got.Builtin || got.PluginID != "" {
		t.Errorf("theme = %+v", got)
	}
	if !got.InstalledAt.Equal(coretest.Epoch) {
		t.Errorf("InstalledAt = %v, want the clock's time", got.InstalledAt)
	}
	stored, err := env.themes.Get(ctx, got.ID)
	if err != nil || stored.Name != "Ocean" {
		t.Fatalf("Get() = %+v, %v", stored, err)
	}
	entries := env.audit.Entries()
	if len(entries) != 1 || entries[0].Action != "theme.import" || entries[0].Target != "theme:"+got.ID || entries[0].Actor != "human:ada" {
		t.Fatalf("audit = %+v", entries)
	}
}

func TestThemes_ImportRefusals(t *testing.T) {
	t.Parallel()
	env := newThemeEnv(t)
	ctx := human("ada")
	if _, err := env.themes.Import(ctx, draftDocument(t, named("Ocean", "1.0.0"))); err != nil {
		t.Fatal(err)
	}

	_, dup := env.themes.Import(ctx, draftDocument(t, named("Ocean", "2.0.0")))
	if !errors.Is(dup, core.ErrConflict) || !errors.Is(dup, domain.ErrAlreadyInstalled) {
		t.Errorf("duplicate name: error = %v, want conflict", dup)
	}
	_, bad := env.themes.Import(ctx, []byte(`{"name":"x"}`))
	if !errors.Is(bad, core.ErrInvalid) || !errors.Is(bad, domain.ErrInvalidTheme) {
		t.Errorf("invalid theme: error = %v, want invalid", bad)
	}
	_, big := env.themes.Import(ctx, make([]byte, domain.MaxThemeBytes+1))
	if p := core.ProblemFrom(big); p.Status != http.StatusRequestEntityTooLarge || !errors.Is(big, domain.ErrTooLarge) {
		t.Errorf("too large: problem = %+v, error = %v", p, big)
	}
	if n := len(env.audit.Entries()); n != 1 {
		t.Errorf("refused imports were audited: %d entries", n)
	}
}

type failingAudit struct{}

func (failingAudit) Append(context.Context, core.AuditEntry) error { return errors.New("disk full") }

func TestThemes_ImportFailsWhenTheAuditLogFails(t *testing.T) {
	t.Parallel()
	themes := app.NewThemes(memory.NewThemes(), failingAudit{}, coretest.NewClock(coretest.Epoch), coretest.NewIDs())
	if _, err := themes.Import(human("ada"), draftDocument(t, nil)); err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("Import() error = %v, want the audit error", err)
	}
}

func TestThemes_ListPagesWithACursor(t *testing.T) {
	t.Parallel()
	env := newThemeEnv(t)
	ctx := human("ada")
	for _, name := range []string{"A", "B", "C"} {
		if _, err := env.themes.Import(ctx, draftDocument(t, named("Theme "+name, "1.0.0"))); err != nil {
			t.Fatal(err)
		}
	}

	first, err := env.themes.List(ctx, "", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Items) != 2 || first.NextCursor == "" || first.Items[0].Name != "Theme A" {
		t.Fatalf("first page = %+v", first)
	}
	second, err := env.themes.List(ctx, first.NextCursor, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Items) != 1 || second.NextCursor != "" || second.Items[0].Name != "Theme C" {
		t.Fatalf("second page = %+v", second)
	}
}

func TestThemes_ListLimits(t *testing.T) {
	t.Parallel()
	env := newThemeEnv(t)
	if _, err := env.themes.List(human("ada"), "garbage", 10); !errors.Is(err, core.ErrInvalid) {
		t.Errorf("bad cursor: error = %v, want invalid", err)
	}
	page, err := env.themes.List(human("ada"), "", 0)
	if err != nil || page.Items == nil || len(page.Items) != 0 {
		t.Errorf("default limit on an empty repository = %+v, %v", page, err)
	}
	if _, err := env.themes.List(human("ada"), "", 100000); err != nil {
		t.Errorf("huge limit: error = %v, want it clamped", err)
	}
}

func TestThemes_GetMissingIsNotFound(t *testing.T) {
	t.Parallel()
	env := newThemeEnv(t)
	if _, err := env.themes.Get(human("ada"), "thm_missing"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("Get() error = %v, want not found", err)
	}
}

func TestThemes_DeleteRules(t *testing.T) {
	t.Parallel()
	env := newThemeEnv(t)
	ctx := human("ada")
	builtin := seedBuiltin(t, env)
	custom, err := env.themes.Import(ctx, draftDocument(t, named("Ocean", "1.0.0")))
	if err != nil {
		t.Fatal(err)
	}

	if err := env.themes.Delete(ctx, builtin.ID); !errors.Is(err, core.ErrConflict) || !errors.Is(err, domain.ErrBuiltin) {
		t.Errorf("builtin: error = %v, want conflict", err)
	}
	if err := env.themes.Delete(ctx, "thm_missing"); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("missing: error = %v, want not found", err)
	}
	if _, err := env.themes.SetSelection(human("bob"), &custom.ID, nil); err != nil {
		t.Fatal(err)
	}
	if err := env.themes.Delete(ctx, custom.ID); !errors.Is(err, core.ErrConflict) || !errors.Is(err, domain.ErrInUse) {
		t.Errorf("in use: error = %v, want conflict", err)
	}
	other := builtin.ID
	if _, err := env.themes.SetSelection(human("bob"), &other, nil); err != nil {
		t.Fatal(err)
	}
	if err := env.themes.Delete(ctx, custom.ID); err != nil {
		t.Fatalf("Delete() of a free theme: %v", err)
	}
	last := env.audit.Entries()[len(env.audit.Entries())-1]
	if last.Action != "theme.delete" || last.Target != "theme:"+custom.ID {
		t.Errorf("last audit entry = %+v", last)
	}
}

func TestThemes_SelectionDefaultsToTheBuiltInThemeInSystemMode(t *testing.T) {
	t.Parallel()
	env := newThemeEnv(t)
	if _, err := env.themes.Selection(human("ada")); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("before the built-in theme exists: error = %v, want not found", err)
	}
	builtin := seedBuiltin(t, env)

	got, err := env.themes.Selection(human("ada"))
	if err != nil {
		t.Fatal(err)
	}
	if got.ThemeID != builtin.ID || got.Mode != domain.ModeSystem {
		t.Fatalf("Selection() = %+v", got)
	}
}

func TestThemes_SetSelectionChangesThemeModeOrBoth(t *testing.T) {
	t.Parallel()
	env := newThemeEnv(t)
	builtin := seedBuiltin(t, env)
	ocean, err := env.themes.Import(human("ada"), draftDocument(t, named("Ocean", "1.0.0")))
	if err != nil {
		t.Fatal(err)
	}
	ctx := human("ada")
	dark, light := domain.ModeDark, domain.ModeLight

	got, err := env.themes.SetSelection(ctx, nil, &dark)
	if err != nil || got.ThemeID != builtin.ID || got.Mode != domain.ModeDark {
		t.Fatalf("mode only = %+v, %v", got, err)
	}
	got, err = env.themes.SetSelection(ctx, &ocean.ID, nil)
	if err != nil || got.ThemeID != ocean.ID || got.Mode != domain.ModeDark {
		t.Fatalf("theme only keeps the mode = %+v, %v", got, err)
	}
	got, err = env.themes.SetSelection(ctx, &builtin.ID, &light)
	if err != nil || got.ThemeID != builtin.ID || got.Mode != domain.ModeLight {
		t.Fatalf("both = %+v, %v", got, err)
	}
	if again, _ := env.themes.Selection(ctx); again != got {
		t.Errorf("Selection() = %+v, want %+v", again, got)
	}
	if other, _ := env.themes.Selection(human("bob")); other.Mode != domain.ModeSystem {
		t.Errorf("another human's selection changed: %+v", other)
	}
	last := env.audit.Entries()[len(env.audit.Entries())-1]
	if last.Action != "theme.select" || last.Actor != "human:ada" {
		t.Errorf("last audit entry = %+v", last)
	}
}

func TestThemes_SetSelectionRefusals(t *testing.T) {
	t.Parallel()
	env := newThemeEnv(t)
	seedBuiltin(t, env)
	missing, bad := "thm_missing", domain.Mode("sepia")

	if _, err := env.themes.SetSelection(human("ada"), &missing, nil); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("unknown theme: error = %v, want not found", err)
	}
	if _, err := env.themes.SetSelection(human("ada"), nil, &bad); !errors.Is(err, core.ErrInvalid) {
		t.Errorf("bad mode: error = %v, want invalid", err)
	}
	if _, err := env.themes.SetSelection(human("ada"), nil, nil); !errors.Is(err, core.ErrInvalid) {
		t.Errorf("nothing to change: error = %v, want invalid", err)
	}
}

func TestThemes_SelectionBelongsToASignedInHuman(t *testing.T) {
	t.Parallel()
	env := newThemeEnv(t)
	seedBuiltin(t, env)
	agent := core.WithCaller(context.Background(), core.Caller{Kind: core.CallerAgent, Name: "founder", UID: 1001})
	dark := domain.ModeDark

	if _, err := env.themes.Selection(context.Background()); !errors.Is(err, core.ErrUnauthorized) {
		t.Errorf("no caller: error = %v, want unauthorized", err)
	}
	if _, err := env.themes.Selection(agent); !errors.Is(err, core.ErrUnauthorized) {
		t.Errorf("agent: error = %v, want unauthorized", err)
	}
	if _, err := env.themes.SetSelection(agent, nil, &dark); !errors.Is(err, core.ErrUnauthorized) {
		t.Errorf("agent set: error = %v, want unauthorized", err)
	}
	if _, err := env.themes.SetSelection(context.Background(), nil, &dark); !errors.Is(err, core.ErrUnauthorized) {
		t.Errorf("no caller set: error = %v, want unauthorized", err)
	}
}

func TestThemes_EnsureBuiltinIsIdempotentAndFollowsNewVersions(t *testing.T) {
	t.Parallel()
	env := newThemeEnv(t)
	ctx := context.Background()

	first, err := env.themes.EnsureBuiltin(ctx, draftDocument(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	again, err := env.themes.EnsureBuiltin(ctx, draftDocument(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	if !first.Builtin || again.ID != first.ID || len(env.audit.Entries()) != 1 {
		t.Fatalf("first %+v again %+v, audit %d", first, again, len(env.audit.Entries()))
	}
	if a := env.audit.Entries()[0]; a.Action != "theme.import" || a.Actor != "system:plugins" {
		t.Errorf("audit = %+v", a)
	}

	env.clock.Advance(time.Hour)
	newer, err := env.themes.EnsureBuiltin(ctx, draftDocument(t, func(d map[string]any) { d["version"] = "0.2.0" }))
	if err != nil {
		t.Fatal(err)
	}
	if newer.ID != first.ID || newer.Version != "0.2.0" || !newer.InstalledAt.Equal(first.InstalledAt) {
		t.Errorf("after a new version = %+v", newer)
	}
}

func TestThemes_EnsureBuiltinRefusals(t *testing.T) {
	t.Parallel()
	env := newThemeEnv(t)
	ctx := human("ada")
	if _, err := env.themes.Import(ctx, draftDocument(t, nil)); err != nil {
		t.Fatal(err)
	}
	if _, err := env.themes.EnsureBuiltin(ctx, draftDocument(t, nil)); !errors.Is(err, core.ErrConflict) {
		t.Errorf("name taken by an import: error = %v, want conflict", err)
	}
	if _, err := env.themes.EnsureBuiltin(ctx, []byte("{")); !errors.Is(err, core.ErrInvalid) {
		t.Errorf("not a theme: error = %v, want invalid", err)
	}
}

func TestThemes_ImportFromPluginInstallsUpdatesAndRefusesAClash(t *testing.T) {
	t.Parallel()
	env := newThemeEnv(t)
	ctx := context.Background()
	const plugin, other = "plg_01JD3Q8Z5X6Y7W8V9T0S1R2Q3P", "plg_01JD3Q8Z5X6Y7W8V9T0S1R2Q3Q"

	first, err := env.themes.ImportFromPlugin(ctx, plugin, draftDocument(t, named("Dusk", "1.0.0")))
	if err != nil || first.PluginID != plugin || first.Builtin {
		t.Fatalf("install = %+v, %v", first, err)
	}
	same, err := env.themes.ImportFromPlugin(ctx, plugin, draftDocument(t, named("Dusk", "1.0.0")))
	if err != nil || same.ID != first.ID {
		t.Fatalf("same version = %+v, %v", same, err)
	}
	newer, err := env.themes.ImportFromPlugin(ctx, plugin, draftDocument(t, named("Dusk", "1.1.0")))
	if err != nil || newer.ID != first.ID || newer.Version != "1.1.0" {
		t.Fatalf("new version = %+v, %v", newer, err)
	}
	if _, err := env.themes.ImportFromPlugin(ctx, other, draftDocument(t, named("Dusk", "9.0.0"))); !errors.Is(err, core.ErrConflict) {
		t.Errorf("clash with another plugin: error = %v, want conflict", err)
	}
	if _, err := env.themes.ImportFromPlugin(ctx, plugin, []byte("{")); !errors.Is(err, core.ErrInvalid) {
		t.Errorf("not a theme: error = %v, want invalid", err)
	}
}

func TestThemes_RemoveFromPluginDeletesItsThemesAndMovesHumansToTheBuiltInTheme(t *testing.T) {
	t.Parallel()
	env := newThemeEnv(t)
	ctx := context.Background()
	const plugin = "plg_01JD3Q8Z5X6Y7W8V9T0S1R2Q3P"
	builtin := seedBuiltin(t, env)
	dusk, err := env.themes.ImportFromPlugin(ctx, plugin, draftDocument(t, named("Dusk", "1.0.0")))
	if err != nil {
		t.Fatal(err)
	}
	light := domain.ModeLight
	if _, err := env.themes.SetSelection(human("ada"), &dusk.ID, &light); err != nil {
		t.Fatal(err)
	}

	if err := env.themes.RemoveFromPlugin(ctx, plugin); err != nil {
		t.Fatalf("RemoveFromPlugin() error = %v", err)
	}

	if _, err := env.themes.Get(ctx, dusk.ID); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("theme still there: %v", err)
	}
	got, err := env.themes.Selection(human("ada"))
	if err != nil || got.ThemeID != builtin.ID || got.Mode != domain.ModeLight {
		t.Errorf("Selection() = %+v, %v, want the built-in theme with the mode kept", got, err)
	}
	if err := env.themes.RemoveFromPlugin(ctx, plugin); err != nil {
		t.Errorf("second RemoveFromPlugin() error = %v, want it idempotent", err)
	}
}

func TestThemes_RemoveFromPluginWithoutABuiltInThemeRefusesWhileSelected(t *testing.T) {
	t.Parallel()
	env := newThemeEnv(t)
	ctx := context.Background()
	const plugin = "plg_01JD3Q8Z5X6Y7W8V9T0S1R2Q3P"
	dusk, err := env.themes.ImportFromPlugin(ctx, plugin, draftDocument(t, named("Dusk", "1.0.0")))
	if err != nil {
		t.Fatal(err)
	}
	if err := env.repo.SetSelection(ctx, "ada", domain.Selection{ThemeID: dusk.ID, Mode: domain.ModeSystem}); err != nil {
		t.Fatal(err)
	}
	if err := env.themes.RemoveFromPlugin(ctx, plugin); !errors.Is(err, core.ErrConflict) {
		t.Fatalf("RemoveFromPlugin() error = %v, want conflict", err)
	}
}
