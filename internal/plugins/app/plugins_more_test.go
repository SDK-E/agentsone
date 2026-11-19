// SPDX-License-Identifier: MIT

package app_test

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"
	"testing"
	"testing/fstest"
	"time"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/core/coretest"
	"github.com/SDK-E/agentsone/internal/plugins/adapters/fsinstall"
	"github.com/SDK-E/agentsone/internal/plugins/adapters/memory"
	"github.com/SDK-E/agentsone/internal/plugins/app"
	"github.com/SDK-E/agentsone/internal/plugins/domain"
)

// specFetcher records the spec it was asked for and answers with a tree or an error.
type specFetcher struct {
	specs []domain.FetchSpec
	files fstest.MapFS
	err   error
}

func (f *specFetcher) Fetch(_ context.Context, spec domain.FetchSpec) (domain.Tree, error) {
	f.specs = append(f.specs, spec)
	if f.err != nil {
		return domain.Tree{}, f.err
	}
	return domain.Tree{FS: f.files, Cleanup: func() {}}, nil
}

type fakeRegistry struct {
	srv domain.RegistryServer
	err error
}

func (f fakeRegistry) Server(context.Context, string, string) (domain.RegistryServer, error) {
	return f.srv, f.err
}

type fakeCatalog struct{ entries []domain.CatalogEntry }

func (f fakeCatalog) Search(context.Context, domain.CatalogQuery) ([]domain.CatalogEntry, error) {
	return f.entries, nil
}

func depsWith(t *testing.T, mutate func(*app.PluginsDeps)) (*app.Plugins, *memory.Plugins, *fakeThemeHook) {
	t.Helper()
	clock := coretest.NewClock(coretest.Epoch)
	ids := coretest.NewIDs()
	repo, themes := memory.NewPlugins(), &fakeThemeHook{}
	d := app.PluginsDeps{
		Repo: repo, Installer: fsinstall.New(t.TempDir(), domain.InstallLimits{}), Audit: coretest.NewAudit(),
		Clock: clock, IDs: ids, Themes: themes, Tokens: coretest.NewTokens(clock, ids), LibDir: "/usr/lib/agentsone",
	}
	mutate(&d)
	return app.NewPlugins(d), repo, themes
}

func TestPlugins_RefsAreRoutedToTheRightFetcher(t *testing.T) {
	t.Parallel()
	tests := []struct {
		ref     string
		version string
		want    domain.FetchSpec
	}{
		{"npm:@acme/skills", "1.2.0", domain.FetchSpec{Kind: domain.FetchNPM, Ref: "@acme/skills", Rev: "1.2.0"}},
		{"https://example.com/x.tar.gz", "", domain.FetchSpec{Kind: domain.FetchURL, Ref: "https://example.com/x.tar.gz"}},
		{"https://example.com/x/y.git", "v1", domain.FetchSpec{Kind: domain.FetchGit, Ref: "https://example.com/x/y.git", Rev: "v1"}},
		{"git@example.com:x/y.git", "", domain.FetchSpec{Kind: domain.FetchGit, Ref: "git@example.com:x/y.git"}},
		{"acme/skills", "main", domain.FetchSpec{Kind: domain.FetchGitHub, Ref: "acme/skills", Rev: "main"}},
		{"./local/dir", "", domain.FetchSpec{Kind: domain.FetchLocal, Ref: "./local/dir"}},
		{"plainname", "", domain.FetchSpec{Kind: domain.FetchLocal, Ref: "plainname"}},
	}
	for _, tt := range tests {
		t.Run(tt.ref, func(t *testing.T) {
			t.Parallel()
			f := &specFetcher{err: errors.New("stop here")}
			svc, _, _ := depsWith(t, func(d *app.PluginsDeps) { d.Fetcher = f })
			_, _ = svc.Install(humanCtx(), app.InstallInput{Source: "agent-skills", Ref: tt.ref, Version: tt.version})
			if len(f.specs) != 1 || f.specs[0] != tt.want {
				t.Errorf("specs = %+v, want %+v", f.specs, tt.want)
			}
		})
	}
}

func nativeTree() fstest.MapFS {
	manifest := `{
  "name": "bright", "version": "1.0.0", "description": "A bright theme.", "author": {"name": "Acme"}, "license": "MIT",
  "source": {"kind": "agentsone", "ref": "x"}, "compat": {"agentsone": ">=0.1.0"},
  "contents": {"mcpServers": [], "skills": [], "agentTemplates": [], "runners": [],
    "panels": [{"name": "board", "title": "Board", "icon": "./panel/icon.svg", "entry": "./panel/index.html", "scopes": ["tasks:read", "chat:read"]}],
    "themes": [{"path": "./themes/bright.json"}], "emitters": []}
}`
	return fstest.MapFS{
		"agentsone.plugin.json": {Data: []byte(manifest)},
		"themes/bright.json":    {Data: []byte(`{"name":"bright"}`)},
		"panel/index.html":      {Data: []byte("<html></html>")},
		"panel/icon.svg":        {Data: []byte("<svg/>")},
	}
}

func TestPlugins_NativePluginEnableRegistersThemesAndPanelTokenUsesScopes(t *testing.T) {
	t.Parallel()
	f := &specFetcher{files: nativeTree()}
	svc, _, themes := depsWith(t, func(d *app.PluginsDeps) { d.Fetcher = f })

	p, err := svc.Install(humanCtx(), app.InstallInput{Source: "agentsone", Ref: "acme/bright", Enable: true})
	if err != nil {
		t.Fatalf("Install() error = %v", err)
	}

	if len(themes.imported) != 1 || themes.imported[0] != p.ID {
		t.Errorf("theme not registered: %v", themes.imported)
	}
	tok, err := svc.IssuePanelToken(humanCtx(), p.ID, 2*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if tok.Subject != "plugin:"+p.ID || len(tok.Scopes) != 2 || tok.Scopes[0] != "chat:read" || tok.Scopes[1] != "tasks:read" {
		t.Errorf("token = %+v", tok)
	}
	if tok.ExpiresAt.Sub(tok.CreatedAt) != 2*time.Hour {
		t.Errorf("lifetime = %s", tok.ExpiresAt.Sub(tok.CreatedAt))
	}
	def, err := svc.IssuePanelToken(humanCtx(), p.ID, 0)
	if err != nil || def.ExpiresAt.Sub(def.CreatedAt) != time.Hour {
		t.Errorf("default ttl token = %+v, %v", def, err)
	}
}

func TestPlugins_NativeSourceWithoutManifestIsNothingToInstall(t *testing.T) {
	t.Parallel()
	f := &specFetcher{files: fstest.MapFS{"README.md": {Data: []byte("x")}}}
	svc, _, _ := depsWith(t, func(d *app.PluginsDeps) { d.Fetcher = f })
	_, err := svc.Install(humanCtx(), app.InstallInput{Source: "agentsone", Ref: "acme/none"})
	if !errors.Is(err, domain.ErrNothingToInstall) || !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("error = %v, want ErrNothingToInstall and core.ErrInvalid", err)
	}
}

func TestPlugins_ClaudeCodePluginInstalls(t *testing.T) {
	t.Parallel()
	f := &specFetcher{files: claudeTreeForApp()}
	svc, _, _ := depsWith(t, func(d *app.PluginsDeps) { d.Fetcher = f })
	p, err := svc.Install(humanCtx(), app.InstallInput{Source: "claude-code", Ref: "acme/garden-kit"})
	if err != nil || p.Name != "garden-kit" || p.Source != "claude-code" {
		t.Fatalf("Install() = %+v, %v", p, err)
	}
}

func claudeTreeForApp() fstest.MapFS {
	return fstest.MapFS{
		".claude-plugin/plugin.json": {Data: []byte(`{"name":"garden-kit","version":"1.0.0","description":"Garden.","license":"MIT"}`)},
		"skills/prune/SKILL.md":      {Data: []byte("---\nname: prune\ndescription: Prune.\n---\nx")},
	}
}

func TestPlugins_MCPRegistryInstallNeedsARegistryAndRecordsTheServer(t *testing.T) {
	t.Parallel()
	in := app.InstallInput{Source: "mcp-registry", Ref: "io.example/weather@1.0.0"}

	without, _, _ := depsWith(t, func(*app.PluginsDeps) {})
	if _, err := without.Install(humanCtx(), in); !errors.Is(err, core.ErrUnavailable) {
		t.Fatalf("no registry: error = %v, want unavailable", err)
	}

	srv := domain.RegistryServer{
		Name: "io.example/weather", Version: "1.0.0", Description: "Weather.",
		Packages: []domain.RegistryPackage{{RegistryType: "npm", Identifier: "weather", Version: "1.0.0", Transport: domain.RegistryTransport{Type: "stdio"}}},
	}
	with, _, _ := depsWith(t, func(d *app.PluginsDeps) { d.Registry = fakeRegistry{srv: srv} })
	p, err := with.Install(humanCtx(), in)
	if err != nil || p.Name != "weather" || p.Kinds[0] != "mcp-server" {
		t.Fatalf("Install() = %+v, %v", p, err)
	}

	failing, _, _ := depsWith(t, func(d *app.PluginsDeps) { d.Registry = fakeRegistry{err: core.ErrNotFound} })
	if _, err := failing.Install(humanCtx(), in); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("registry error = %v", err)
	}
}

func TestPlugins_InstallWithoutACallerIsUnauthorized(t *testing.T) {
	t.Parallel()
	svc, _, _ := depsWith(t, func(d *app.PluginsDeps) { d.Fetcher = &specFetcher{} })
	if _, err := svc.Install(context.Background(), app.InstallInput{Source: "agentsone", Ref: "a/b"}); !errors.Is(err, core.ErrUnauthorized) {
		t.Fatalf("error = %v", err)
	}
}

func TestPlugins_ListPagesAndFilters(t *testing.T) {
	t.Parallel()
	svc, repo, _ := depsWith(t, func(*app.PluginsDeps) {})
	for i, name := range []string{"aa", "bb", "cc"} {
		p := plugin(name, "1.0.0", func(m *domain.Manifest) {
			m.Contents.Runners = []domain.Runner{{Name: "r-" + name, Command: "x", ProtocolVersions: []string{"1.0"}}}
		})
		p.ID = "plg_0000000000000000000000000" + string(rune('1'+i))
		p.Enabled = i != 1
		p.Kinds = []string{"runner"}
		if err := repo.Insert(context.Background(), p); err != nil {
			t.Fatal(err)
		}
	}

	first, next, err := svc.List(humanCtx(), domain.PluginFilter{}, "", 2)
	if err != nil || len(first) != 2 || next != first[1].ID {
		t.Fatalf("first page = %d items, next %q, %v", len(first), next, err)
	}
	rest, next, _ := svc.List(humanCtx(), domain.PluginFilter{}, next, 2)
	if len(rest) != 1 || next != "" {
		t.Errorf("second page = %d items, next %q", len(rest), next)
	}
	on := true
	enabled, _, _ := svc.List(humanCtx(), domain.PluginFilter{Enabled: &on}, "", 0)
	if len(enabled) != 2 {
		t.Errorf("enabled = %d", len(enabled))
	}
	infos, _ := svc.RunnerEntries(context.Background())
	if len(infos) != 4 { // echo, openai and the two enabled plugin runners
		t.Errorf("runner entries = %d", len(infos))
	}
	if e, err := svc.RunnerEntry(context.Background(), "r-aa"); err != nil || e.PluginID == "" || e.Builtin {
		t.Errorf("RunnerEntry = %+v, %v", e, err)
	}
	if _, err := svc.RunnerEntry(context.Background(), "ghost"); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("RunnerEntry ghost error = %v", err)
	}
	if got, _ := svc.Runners(context.Background()); len(got) != 4 || got[0].Source == "" {
		t.Errorf("Runners = %+v", got)
	}
	ad, _ := svc.Runner(context.Background(), "echo")
	if ad.Info().Source != "builtin" {
		t.Errorf("echo info = %+v", ad.Info())
	}
}

func TestPlugins_CatalogMergesEnabledSourcesAndMarksInstalled(t *testing.T) {
	t.Parallel()
	entries := []domain.CatalogEntry{
		{Source: "agent-skills", Ref: "b/two", Name: "two", Kinds: []string{"skill"}},
		{Source: "agent-skills", Ref: "a/one", Name: "one", Kinds: []string{"runner"}},
	}
	svc, repo, _ := depsWith(t, func(d *app.PluginsDeps) {
		d.Sources = []domain.SourceInfo{
			{ID: "agent-skills", Name: "Skills", URL: "https://s.example.com", Enabled: true},
			{ID: "agentsone", Name: "Own", URL: "https://o.example.com", Enabled: false},
		}
		d.Catalogs = map[string]app.CatalogProvider{"agent-skills": fakeCatalog{entries: entries}, "agentsone": fakeCatalog{entries: entries}}
	})
	installed := plugin("one", "1.0.0", func(*domain.Manifest) {})
	if err := repo.Insert(context.Background(), installed); err != nil {
		t.Fatal(err)
	}

	got, next, err := svc.Catalog(humanCtx(), domain.CatalogQuery{Limit: 1})
	if err != nil || len(got) != 1 || got[0].Ref != "a/one" || !got[0].Installed || next != "a/one" {
		t.Fatalf("page 1 = %+v next %q, %v", got, next, err)
	}
	rest, next, _ := svc.Catalog(humanCtx(), domain.CatalogQuery{After: next, Limit: 5})
	if len(rest) != 1 || rest[0].Ref != "b/two" || rest[0].Installed || next != "" {
		t.Errorf("page 2 = %+v next %q", rest, next)
	}
	byKind, _, _ := svc.Catalog(humanCtx(), domain.CatalogQuery{Kind: "skill"})
	if len(byKind) != 1 || byKind[0].Name != "two" {
		t.Errorf("by kind = %+v", byKind)
	}
	srcs, _ := svc.Sources(humanCtx())
	if len(srcs) != 2 {
		t.Errorf("sources = %+v", srcs)
	}
}

func TestPlugins_ThemePathOutsideThePluginIsRefusedOnEnable(t *testing.T) {
	t.Parallel()
	svc, repo, _ := depsWith(t, func(*app.PluginsDeps) {})
	dir := t.TempDir()
	sum, err := fsinstall.TreeDigest(dir)
	if err != nil {
		t.Fatal(err)
	}
	p := plugin("evil", "1.0.0", func(m *domain.Manifest) { m.Contents.Themes = []domain.FileRef{{Path: "./../../etc/passwd"}} })
	p.Dir, p.SHA256, p.Enabled = dir, sum, false
	if err := repo.Insert(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Enable(humanCtx(), p.ID); !errors.Is(err, domain.ErrUnsafePath) {
		t.Fatalf("error = %v, want ErrUnsafePath", err)
	}
	_ = fs.ValidPath
	_ = filepath.Join
}
