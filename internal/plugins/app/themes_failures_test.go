// SPDX-License-Identifier: MIT

package app_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/core/coretest"
	"github.com/SDK-E/agentsone/internal/plugins/adapters/memory"
	"github.com/SDK-E/agentsone/internal/plugins/app"
	"github.com/SDK-E/agentsone/internal/plugins/domain"
)

// flakyRepo fails one named repository method and delegates the rest.
type flakyRepo struct {
	domain.ThemeRepository
	failOn string
}

var errBoom = errors.New("boom")

func (f flakyRepo) fail(name string) error {
	if f.failOn == name {
		return errBoom
	}
	return nil
}

func (f flakyRepo) Insert(ctx context.Context, t domain.InstalledTheme) error {
	if err := f.fail("Insert"); err != nil {
		return err
	}
	return f.ThemeRepository.Insert(ctx, t)
}

func (f flakyRepo) Update(ctx context.Context, t domain.InstalledTheme) error {
	if err := f.fail("Update"); err != nil {
		return err
	}
	return f.ThemeRepository.Update(ctx, t)
}

func (f flakyRepo) Get(ctx context.Context, id string) (domain.InstalledTheme, error) {
	if err := f.fail("Get"); err != nil {
		return domain.InstalledTheme{}, err
	}
	return f.ThemeRepository.Get(ctx, id)
}

func (f flakyRepo) FindByName(ctx context.Context, name string) (domain.InstalledTheme, bool, error) {
	if err := f.fail("FindByName"); err != nil {
		return domain.InstalledTheme{}, false, err
	}
	return f.ThemeRepository.FindByName(ctx, name)
}

func (f flakyRepo) Builtin(ctx context.Context) (domain.InstalledTheme, bool, error) {
	if err := f.fail("Builtin"); err != nil {
		return domain.InstalledTheme{}, false, err
	}
	return f.ThemeRepository.Builtin(ctx)
}

func (f flakyRepo) ByPlugin(ctx context.Context, id string) ([]domain.InstalledTheme, error) {
	if err := f.fail("ByPlugin"); err != nil {
		return nil, err
	}
	return f.ThemeRepository.ByPlugin(ctx, id)
}

func (f flakyRepo) List(ctx context.Context, after string, limit int) ([]domain.InstalledTheme, error) {
	if err := f.fail("List"); err != nil {
		return nil, err
	}
	return f.ThemeRepository.List(ctx, after, limit)
}

func (f flakyRepo) Delete(ctx context.Context, id string) error {
	if err := f.fail("Delete"); err != nil {
		return err
	}
	return f.ThemeRepository.Delete(ctx, id)
}

func (f flakyRepo) Selection(ctx context.Context, human string) (domain.Selection, bool, error) {
	if err := f.fail("Selection"); err != nil {
		return domain.Selection{}, false, err
	}
	return f.ThemeRepository.Selection(ctx, human)
}

func (f flakyRepo) SetSelection(ctx context.Context, human string, s domain.Selection) error {
	if err := f.fail("SetSelection"); err != nil {
		return err
	}
	return f.ThemeRepository.SetSelection(ctx, human, s)
}

func (f flakyRepo) ReassignSelections(ctx context.Context, from, to string) error {
	if err := f.fail("ReassignSelections"); err != nil {
		return err
	}
	return f.ThemeRepository.ReassignSelections(ctx, from, to)
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

const failPlugin = "plg_01JD3Q8Z5X6Y7W8V9T0S1R2Q3P"

func insertTheme(t *testing.T, base *memory.Themes, id, name string, builtin bool, pluginID string) {
	t.Helper()
	must(t, base.Insert(context.Background(), domain.InstalledTheme{ID: id, Name: name, Version: "1.0.0", Builtin: builtin, PluginID: pluginID}))
}

func TestThemes_RepositoryFailuresSurfaceAsServerErrors(t *testing.T) {
	t.Parallel()
	dark := domain.ModeDark
	ctx := human("a")
	tests := []struct {
		name   string
		failOn string
		run    func(th *app.Themes, base *memory.Themes) error
	}{
		{"list", "List", func(th *app.Themes, _ *memory.Themes) error { _, err := th.List(ctx, "", 1); return err }},
		{"get", "Get", func(th *app.Themes, _ *memory.Themes) error { _, err := th.Get(ctx, "thm_x"); return err }},
		{"import", "Insert", func(th *app.Themes, _ *memory.Themes) error {
			_, err := th.Import(ctx, draftDocument(t, nil))
			return err
		}},
		{"ensure builtin", "FindByName", func(th *app.Themes, _ *memory.Themes) error {
			_, err := th.EnsureBuiltin(ctx, draftDocument(t, nil))
			return err
		}},
		{"import from plugin", "FindByName", func(th *app.Themes, _ *memory.Themes) error {
			_, err := th.ImportFromPlugin(ctx, failPlugin, draftDocument(t, nil))
			return err
		}},
		{"remove: by plugin", "ByPlugin", func(th *app.Themes, _ *memory.Themes) error { return th.RemoveFromPlugin(ctx, failPlugin) }},
		{"remove: builtin lookup", "Builtin", func(th *app.Themes, _ *memory.Themes) error { return th.RemoveFromPlugin(ctx, failPlugin) }},
		{"selection: builtin lookup", "Builtin", func(th *app.Themes, _ *memory.Themes) error { _, err := th.Selection(ctx); return err }},
		{"selection: lookup", "Selection", func(th *app.Themes, _ *memory.Themes) error { _, err := th.Selection(ctx); return err }},
		{"set selection", "SetSelection", func(th *app.Themes, base *memory.Themes) error {
			insertTheme(t, base, "thm_0", "Base", true, "")
			_, err := th.SetSelection(ctx, nil, &dark)
			return err
		}},
		{"delete", "Delete", func(th *app.Themes, base *memory.Themes) error {
			insertTheme(t, base, "thm_1", "One", false, "")
			return th.Delete(ctx, "thm_1")
		}},
		{"remove: reassign", "ReassignSelections", func(th *app.Themes, base *memory.Themes) error {
			insertTheme(t, base, "thm_0", "Base", true, "")
			insertTheme(t, base, "thm_1", "One", false, failPlugin)
			return th.RemoveFromPlugin(ctx, failPlugin)
		}},
		{"remove: delete", "Delete", func(th *app.Themes, base *memory.Themes) error {
			insertTheme(t, base, "thm_1", "One", false, failPlugin)
			return th.RemoveFromPlugin(ctx, failPlugin)
		}},
		{"plugin update", "Update", func(th *app.Themes, _ *memory.Themes) error {
			if _, err := th.ImportFromPlugin(ctx, failPlugin, draftDocument(t, named("Dusk", "1.0.0"))); err != nil {
				return err
			}
			_, err := th.ImportFromPlugin(ctx, failPlugin, draftDocument(t, named("Dusk", "2.0.0")))
			return err
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			base := memory.NewThemes()
			repo := flakyRepo{ThemeRepository: base, failOn: tt.failOn}
			themes := app.NewThemes(repo, coretest.NewAudit(), coretest.NewClock(coretest.Epoch), coretest.NewIDs())
			err := tt.run(themes, base)
			if !errors.Is(err, errBoom) || core.ProblemFrom(err).Status != http.StatusInternalServerError {
				t.Fatalf("error = %v, want boom as a 500", err)
			}
		})
	}
}

func TestThemes_AuditFailureSurfacesFromEveryChange(t *testing.T) {
	t.Parallel()
	ctx := human("ada")
	base := memory.NewThemes()
	good := app.NewThemes(base, coretest.NewAudit(), coretest.NewClock(coretest.Epoch), coretest.NewIDs())
	custom, err := good.Import(ctx, draftDocument(t, named("Ocean", "1.0.0")))
	must(t, err)
	builtin, err := good.EnsureBuiltin(ctx, draftDocument(t, nil))
	must(t, err)
	_, err = good.ImportFromPlugin(ctx, "plg_P", draftDocument(t, named("Dusk", "1.0.0")))
	must(t, err)
	bad := app.NewThemes(base, failingAudit{}, coretest.NewClock(coretest.Epoch), coretest.NewIDs())
	dark := domain.ModeDark

	runs := map[string]func() error{
		"delete": func() error { return bad.Delete(ctx, custom.ID) },
		"select": func() error { _, err := bad.SetSelection(ctx, &builtin.ID, &dark); return err },
		"update": func() error {
			_, err := bad.ImportFromPlugin(ctx, "plg_P", draftDocument(t, named("Dusk", "2.0.0")))
			return err
		},
		"remove": func() error { return bad.RemoveFromPlugin(ctx, "plg_P") },
	}
	for name, run := range runs {
		if err := run(); err == nil || !strings.Contains(err.Error(), "disk full") {
			t.Errorf("%s: error = %v, want the audit error", name, err)
		}
	}
}
