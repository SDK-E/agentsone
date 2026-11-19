// SPDX-License-Identifier: MIT

package app_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/plugins/adapters/fetch"
	"github.com/SDK-E/agentsone/internal/plugins/app"
	"github.com/SDK-E/agentsone/internal/plugins/domain"
)

func bundledManifest(name string) string {
	return `{
  "name": "` + name + `", "version": "1.0.0", "description": "Bundled.", "author": {"name": "Agents One"}, "license": "MIT",
  "source": {"kind": "agentsone", "ref": "bundled"}, "compat": {"agentsone": ">=0.1.0"},
  "contents": {"mcpServers": [], "skills": [{"name": "seed", "path": "./skills/seed", "description": "Seed."}],
    "agentTemplates": [], "runners": [], "panels": [], "themes": [], "emitters": []}
}`
}

func TestBootstrap_InstallsBundledPluginsOnceAndIsIdempotent(t *testing.T) {
	t.Parallel()
	bundled := t.TempDir()
	writeFile(t, filepath.Join(bundled, "small-seed", "agentsone.plugin.json"), bundledManifest("small-seed"))
	writeFile(t, filepath.Join(bundled, "small-seed", "skills", "seed", "SKILL.md"), "---\nname: seed\ndescription: Seed.\n---\nx")
	writeFile(t, filepath.Join(bundled, "README.txt"), "not a plugin")
	svc, repo, _ := depsWith(t, func(d *app.PluginsDeps) { d.Fetcher = fetch.New(fetch.WithTempDir(t.TempDir())) })

	first, err := svc.Bootstrap(context.Background(), bundled, app.BootstrapOptions{})
	if err != nil || len(first) != 1 || first[0] != "small-seed" {
		t.Fatalf("first Bootstrap() = %v, %v", first, err)
	}
	second, err := svc.Bootstrap(context.Background(), bundled, app.BootstrapOptions{})
	if err != nil || len(second) != 0 {
		t.Fatalf("second Bootstrap() = %v, %v, want nothing to install", second, err)
	}

	list, _ := repo.List(context.Background(), domain.PluginFilter{}, "", 10)
	if len(list) != 1 || !list[0].Enabled || list[0].InstalledBy.Kind != string(core.CallerSystem) {
		t.Fatalf("plugins = %+v", list)
	}
}

func TestBootstrap_AMissingFolderIsFine(t *testing.T) {
	t.Parallel()
	svc, _, _ := depsWith(t, func(d *app.PluginsDeps) { d.Fetcher = fetch.New() })
	got, err := svc.Bootstrap(context.Background(), filepath.Join(t.TempDir(), "nope"), app.BootstrapOptions{})
	if err != nil || len(got) != 0 {
		t.Fatalf("Bootstrap() = %v, %v", got, err)
	}
}

func TestBootstrap_TheDefaultThemePluginIsInstalledAndHandedToTheBuiltinHook(t *testing.T) {
	t.Parallel()
	bundled := t.TempDir()
	manifest := `{
  "name": "agents-one-theme", "version": "1.0.0", "description": "Default theme.", "author": {"name": "Agents One"}, "license": "MIT",
  "source": {"kind": "agentsone", "ref": "bundled"}, "compat": {"agentsone": ">=0.1.0"},
  "contents": {"mcpServers": [], "skills": [], "agentTemplates": [], "runners": [], "panels": [],
    "themes": [{"path": "./themes/theme.json"}], "emitters": []}
}`
	writeFile(t, filepath.Join(bundled, "agents-one-theme", "agentsone.plugin.json"), manifest)
	writeFile(t, filepath.Join(bundled, "agents-one-theme", "themes", "theme.json"), `{"name":"Agents One Theme"}`)
	svc, repo, themes := depsWith(t, func(d *app.PluginsDeps) { d.Fetcher = fetch.New(fetch.WithTempDir(t.TempDir())) })
	var seen [][]byte

	_, err := svc.Bootstrap(context.Background(), bundled, app.BootstrapOptions{
		DefaultThemePlugin: "agents-one-theme",
		EnsureBuiltinTheme: func(_ context.Context, raw []byte) error { seen = append(seen, raw); return nil },
	})
	if err != nil {
		t.Fatalf("Bootstrap() error = %v", err)
	}

	if len(seen) != 1 || string(seen[0]) != `{"name":"Agents One Theme"}` {
		t.Errorf("builtin hook got %q", seen)
	}
	if len(themes.imported) != 0 {
		t.Errorf("the theme was also registered by the plugin: %v", themes.imported)
	}
	list, _ := repo.List(context.Background(), domain.PluginFilter{}, "", 10)
	if len(list) != 1 || list[0].Enabled {
		t.Errorf("plugins = %+v", list)
	}
	_ = os.Remove
}
