// SPDX-License-Identifier: MIT

package plugins_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/SDK-E/agentsone/internal/contract/api"
	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/core/coretest"
	"github.com/SDK-E/agentsone/internal/core/moduletest"
	"github.com/SDK-E/agentsone/internal/plugins"
)

const themeFixture = "../../contract/fixtures/schemas/theme/valid/agents-one-theme-draft.json"

func TestModuleOwnsItsTags(t *testing.T) {
	deps, _ := coretest.NewDeps(t)
	m := plugins.New(deps)
	if m.Name() != "plugins" {
		t.Fatalf("name %q", m.Name())
	}
	moduletest.AssertOwnsTags(t, m, "plugins", "runners", "themes")
}

func TestModuleProvidesTheRunnerRegistry(t *testing.T) {
	deps, _ := coretest.NewDeps(t)
	m := plugins.New(deps)
	if err := m.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	var ports core.Ports
	m.Provide(&ports)

	infos, err := ports.Runners.Runners(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 2 || infos[0].Name != "echo" || infos[1].Name != "openai" || infos[0].Source != "builtin" {
		t.Fatalf("runners = %+v", infos)
	}
}

func writeBundled(t *testing.T, libDir string) {
	t.Helper()
	dir := filepath.Join(libDir, "plugins", "agents-one-theme")
	theme, err := os.ReadFile(themeFixture)
	if err != nil {
		t.Fatal(err)
	}
	manifest := `{
  "name": "agents-one-theme", "version": "1.0.0", "description": "Default theme.", "author": {"name": "Agents One"}, "license": "MIT",
  "source": {"kind": "agentsone", "ref": "bundled"}, "compat": {"agentsone": ">=0.1.0"},
  "contents": {"mcpServers": [], "skills": [], "agentTemplates": [], "runners": [], "panels": [],
    "themes": [{"path": "./themes/theme.json"}], "emitters": []}
}`
	for path, data := range map[string][]byte{"agentsone.plugin.json": []byte(manifest), "themes/theme.json": theme} {
		full := filepath.Join(dir, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func get(t *testing.T, mux *http.ServeMux, path string) map[string]any {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, api.BasePath+path, nil)
	req = req.WithContext(core.WithCaller(req.Context(), core.Caller{Kind: core.CallerHuman, Name: "ada", UID: -1}))
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s = %d: %s", path, rec.Code, rec.Body)
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestModuleBootstrapsTheDefaultThemeOnceOnFirstStart(t *testing.T) {
	deps, _ := coretest.NewDeps(t)
	deps.Config.LibDir = t.TempDir()
	writeBundled(t, deps.Config.LibDir)
	m := plugins.New(deps)
	if err := m.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	for range 2 { // the second start must change nothing
		if err := m.Start(context.Background()); err != nil {
			t.Fatalf("Start() error = %v", err)
		}
	}

	mux := moduletest.Mount(m)
	themes := get(t, mux, "/themes")
	items, _ := themes["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("themes = %v", themes)
	}
	if first, _ := items[0].(map[string]any); first["builtin"] != true || first["name"] != "Agents One Theme" {
		t.Errorf("theme = %v", first)
	}
	list := get(t, mux, "/plugins")
	if items, _ := list["items"].([]any); len(items) != 1 {
		t.Errorf("plugins = %v", list)
	}
	runners := get(t, mux, "/runners")
	if items, _ := runners["items"].([]any); len(items) != 2 {
		t.Errorf("runners = %v", runners)
	}
}
