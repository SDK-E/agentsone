// SPDX-License-Identifier: MIT

package app_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/plugins/adapters/fetch"
	"github.com/SDK-E/agentsone/internal/plugins/app"
	"github.com/SDK-E/agentsone/internal/plugins/domain"
)

// templatePlugin writes a native plugin named name whose agent templates
// are files, each a template document by file name.
func templatePlugin(t *testing.T, name string, files map[string]string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	refs := make([]string, 0, len(files))
	for file, doc := range files {
		writeFile(t, filepath.Join(dir, "agents", file), doc)
		refs = append(refs, `{"path": "./agents/`+file+`"}`)
	}
	writeFile(t, filepath.Join(dir, "agentsone.plugin.json"), `{
  "name": "`+name+`", "version": "1.0.0", "description": "Templates.", "author": {"name": "Agents One"}, "license": "MIT",
  "source": {"kind": "agentsone", "ref": "local"}, "compat": {"agentsone": ">=0.1.0"},
  "contents": {"mcpServers": [], "skills": [], "agentTemplates": [`+strings.Join(refs, ",")+`],
    "runners": [], "panels": [], "themes": [], "emitters": []}
}`)
	return dir
}

func templateDoc(name, kind string, root bool) string {
	isRoot := "false"
	if root {
		isRoot = "true"
	}
	return `{"name": "` + name + `", "kind": "` + kind + `", "isRoot": ` + isRoot + `, "runner": {"name": "openai", "config": {}},
  "oneLine": "Works.", "charter": "", "goals": [], "skills": [], "mcpServers": [],
  "limits": {"cpuPercent": 50, "memoryMiB": 256, "tasks": 64}, "wake": {"subscriptions": []}, "schedules": []}`
}

func newTemplateEnv(t *testing.T) *app.Plugins {
	t.Helper()
	svc, _, _ := depsWith(t, func(d *app.PluginsDeps) { d.Fetcher = fetch.New(fetch.WithTempDir(t.TempDir())) })
	return svc
}

func install(t *testing.T, svc *app.Plugins, dir string, enable bool) domain.Plugin {
	t.Helper()
	pl, err := svc.Install(humanCtx(), app.InstallInput{Source: string(domain.SourceAgentsOne), Ref: dir, Enable: enable})
	if err != nil {
		t.Fatalf("Install(%s) error = %v", dir, err)
	}
	return pl
}

func TestTemplates_ListsTheTemplatesOfEnabledPluginsByID(t *testing.T) {
	t.Parallel()
	svc := newTemplateEnv(t)
	install(t, svc, templatePlugin(t, "small-seed", map[string]string{
		"router.json": templateDoc("router", "chat", false), "a-founder.json": templateDoc("founder", "always-on", true),
	}), true)
	catalog := svc.TemplateCatalog()

	list, err := catalog.List(context.Background())

	if err != nil || len(list) != 2 || list[0].ID != "small-seed/founder" || list[1].ID != "small-seed/router" {
		t.Fatalf("List() = %+v, %v", list, err)
	}
	if !list[0].IsRoot || list[0].Kind != core.AgentAlwaysOn || list[0].Runner != "openai" || len(list[0].Raw) == 0 {
		t.Errorf("founder = %+v", list[0])
	}
	got, err := catalog.Get(context.Background(), "small-seed/router")
	if err != nil || got.Name != "router" || got.Kind != core.AgentChat {
		t.Errorf("Get() = %+v, %v", got, err)
	}
	if _, err := catalog.Get(context.Background(), "small-seed/ghost"); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("Get(unknown) error = %v, want ErrNotFound", err)
	}
}

func TestTemplates_ADisabledPluginHidesItsTemplates(t *testing.T) {
	t.Parallel()
	svc := newTemplateEnv(t)
	pl := install(t, svc, templatePlugin(t, "small-seed", map[string]string{"router.json": templateDoc("router", "chat", false)}), true)
	if _, err := svc.Disable(humanCtx(), pl.ID); err != nil {
		t.Fatal(err)
	}

	list, err := svc.TemplateCatalog().List(context.Background())

	if err != nil || len(list) != 0 {
		t.Fatalf("List() = %+v, %v, want none", list, err)
	}
	if _, err := svc.TemplateCatalog().Get(context.Background(), "small-seed/router"); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("Get() error = %v, want ErrNotFound", err)
	}
}

func TestTemplates_AMissingFileIsAPlainError(t *testing.T) {
	t.Parallel()
	svc := newTemplateEnv(t)
	pl := install(t, svc, templatePlugin(t, "small-seed", map[string]string{"router.json": templateDoc("router", "chat", false)}), true)
	if err := os.Remove(filepath.Join(pl.Dir, "agents", "router.json")); err != nil {
		t.Fatal(err)
	}

	_, err := svc.TemplateCatalog().List(context.Background())

	if err == nil || errors.Is(err, core.ErrNotFound) || errors.Is(err, core.ErrInvalid) {
		t.Fatalf("List() error = %v, want a plain error", err)
	}
	if _, err := svc.TemplateCatalog().Get(context.Background(), "small-seed/router"); err == nil || errors.Is(err, core.ErrNotFound) {
		t.Errorf("Get() error = %v, want a plain error", err)
	}
}

func TestTemplates_EnableRefusesADuplicateTemplateName(t *testing.T) {
	t.Parallel()
	svc := newTemplateEnv(t)
	dir := templatePlugin(t, "small-seed", map[string]string{
		"router.json": templateDoc("router", "chat", false), "router-2.json": templateDoc("router", "chat", false),
	})

	_, err := svc.Install(humanCtx(), app.InstallInput{Source: string(domain.SourceAgentsOne), Ref: dir, Enable: true})

	if !errors.Is(err, core.ErrConflict) {
		t.Fatalf("Install() error = %v, want ErrConflict", err)
	}
	if list, _ := svc.TemplateCatalog().List(context.Background()); len(list) != 0 {
		t.Errorf("templates = %+v, want none", list)
	}
}

func TestTemplates_EnableRefusesATemplateAnotherEnabledPluginHas(t *testing.T) {
	t.Parallel()
	svc := newTemplateEnv(t)
	install(t, svc, templatePlugin(t, "small-seed", map[string]string{"router.json": templateDoc("router", "chat", false)}), true)
	newer := templatePlugin(t, "small-seed", map[string]string{"router.json": templateDoc("router", "chat", false)})
	manifest := filepath.Join(newer, "agentsone.plugin.json")
	raw, err := os.ReadFile(manifest) // #nosec G304 -- the test's own temp folder
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, manifest, strings.Replace(string(raw), `"version": "1.0.0"`, `"version": "1.1.0"`, 1))

	pl := install(t, svc, newer, false)
	_, err = svc.Enable(humanCtx(), pl.ID)

	if !errors.Is(err, core.ErrConflict) {
		t.Fatalf("Enable() error = %v, want ErrConflict", err)
	}
}

func TestTemplates_EnableRefusesABadTemplate(t *testing.T) {
	t.Parallel()
	svc := newTemplateEnv(t)
	dir := templatePlugin(t, "small-seed", map[string]string{"router.json": `{"name": "Router"}`})

	_, err := svc.Install(humanCtx(), app.InstallInput{Source: string(domain.SourceAgentsOne), Ref: dir, Enable: true})

	if !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("Install() error = %v, want ErrInvalid", err)
	}
}
