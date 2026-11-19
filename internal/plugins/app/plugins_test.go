// SPDX-License-Identifier: MIT

package app_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/core/coretest"
	"github.com/SDK-E/agentsone/internal/plugins/adapters/fetch"
	"github.com/SDK-E/agentsone/internal/plugins/adapters/fsinstall"
	"github.com/SDK-E/agentsone/internal/plugins/adapters/memory"
	"github.com/SDK-E/agentsone/internal/plugins/app"
	"github.com/SDK-E/agentsone/internal/plugins/domain"
	"github.com/SDK-E/agentsone/internal/plugins/pluginstest"
)

type fakeThemeHook struct{ imported, removed []string }

func (f *fakeThemeHook) ImportFromPlugin(_ context.Context, id string, _ []byte) (domain.InstalledTheme, error) {
	f.imported = append(f.imported, id)
	return domain.InstalledTheme{}, nil
}

func (f *fakeThemeHook) RemoveFromPlugin(_ context.Context, id string) error {
	f.removed = append(f.removed, id)
	return nil
}

type pluginEnv struct {
	svc    *app.Plugins
	repo   *memory.Plugins
	audit  *coretest.Audit
	themes *fakeThemeHook
	tokens *coretest.Tokens
	dir    string // PluginsDir
	src    string // a local agent-skills source tree
}

func humanCtx() context.Context {
	return core.WithCaller(context.Background(), core.Caller{Kind: core.CallerHuman, Name: "ada", UID: -1})
}

func newPluginEnv(t *testing.T) pluginEnv {
	t.Helper()
	root := t.TempDir()
	src := filepath.Join(root, "My_Skills")
	writeFile(t, filepath.Join(src, "skills", "notes", "SKILL.md"), "---\nname: notes\ndescription: Take notes.\n---\nBody")
	writeFile(t, filepath.Join(src, "skills", "todo", "SKILL.md"), "---\nname: todo\ndescription: Keep a todo list.\n---\nBody")
	pluginsDir := filepath.Join(root, "plugins")
	if err := os.MkdirAll(pluginsDir, 0o750); err != nil {
		t.Fatal(err)
	}
	clock := coretest.NewClock(coretest.Epoch)
	ids := coretest.NewIDs()
	env := pluginEnv{
		repo: memory.NewPlugins(), audit: coretest.NewAudit(), themes: &fakeThemeHook{},
		tokens: coretest.NewTokens(clock, ids), dir: pluginsDir, src: src,
	}
	env.svc = app.NewPlugins(app.PluginsDeps{
		Repo: env.repo, Fetcher: fetch.New(fetch.WithTempDir(root)), Installer: fsinstall.New(pluginsDir, domain.InstallLimits{}),
		Audit: env.audit, Clock: clock, IDs: ids, Themes: env.themes, Tokens: env.tokens, LibDir: "/usr/lib/agentsone",
		Sources: []domain.SourceInfo{{ID: "agent-skills", Name: "Agent Skills", URL: "https://example.com/skills", Enabled: true}},
	})
	return env
}

// plugin returns an enabled installed plugin named name with manifest changes.
func plugin(name, version string, change func(*domain.Manifest)) domain.Plugin {
	p := pluginstest.Plugin(7)
	p.Name, p.Version = name, version
	p.Manifest.Name, p.Manifest.Version = name, version
	p.Manifest.Contents.Skills, p.Manifest.Contents.Themes = nil, nil
	change(&p.Manifest)
	return p
}

func writeFile(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestPlugins_InstallFromALocalSourceRecordsAuditsAndEnables(t *testing.T) {
	t.Parallel()
	env := newPluginEnv(t)

	got, err := env.svc.Install(humanCtx(), app.InstallInput{Source: "agent-skills", Ref: env.src, Enable: true})
	if err != nil {
		t.Fatalf("Install() error = %v", err)
	}

	if got.Name != "my-skills" || got.Version != "0.0.0" || !got.Enabled || got.Health != domain.HealthOK {
		t.Errorf("plugin = %+v", got)
	}
	if got.InstalledBy != (domain.Actor{Kind: "human", Name: "ada"}) {
		t.Errorf("installed by = %+v", got.InstalledBy)
	}
	if _, err := os.Stat(filepath.Join(env.dir, "my-skills", "0.0.0", "agentsone.plugin.json")); err != nil {
		t.Errorf("manifest not on disk: %v", err)
	}
	if sum, err := fsinstall.TreeDigest(got.Dir); err != nil || sum != got.SHA256 {
		t.Errorf("digest %q (%v), recorded %q", sum, err, got.SHA256)
	}
	if entries := env.audit.Entries(); len(entries) != 2 || entries[0].Action != "plugin.install" || entries[0].Actor != "human:ada" || entries[1].Action != "plugin.enable" {
		t.Errorf("audit = %+v", entries)
	}
}

func TestPlugins_InstallWithoutEnableLeavesItOff(t *testing.T) {
	t.Parallel()
	env := newPluginEnv(t)
	got, err := env.svc.Install(humanCtx(), app.InstallInput{Source: "agent-skills", Ref: env.src})
	if err != nil {
		t.Fatal(err)
	}
	if got.Enabled {
		t.Error("plugin enabled without being asked")
	}
}

func TestPlugins_InstallTwiceIsAConflict(t *testing.T) {
	t.Parallel()
	env := newPluginEnv(t)
	in := app.InstallInput{Source: "agent-skills", Ref: env.src}
	if _, err := env.svc.Install(humanCtx(), in); err != nil {
		t.Fatal(err)
	}
	if _, err := env.svc.Install(humanCtx(), in); !errors.Is(err, domain.ErrAlreadyInstalled) {
		t.Fatalf("second install error = %v, want ErrAlreadyInstalled", err)
	}
}

func TestPlugins_AnAgentCannotInstallFromALocalPath(t *testing.T) {
	t.Parallel()
	env := newPluginEnv(t)
	ctx := core.WithCaller(context.Background(), core.Caller{Kind: core.CallerAgent, Name: "bob", UID: 1001})

	_, err := env.svc.Install(ctx, app.InstallInput{Source: "agent-skills", Ref: env.src})

	if !errors.Is(err, core.ErrForbidden) {
		t.Fatalf("error = %v, want core.ErrForbidden", err)
	}
	if got, _, _ := env.svc.List(humanCtx(), domain.PluginFilter{}, "", 10); len(got) != 0 {
		t.Errorf("a plugin was installed: %+v", got)
	}
}

func TestPlugins_InstallRefusals(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   app.InstallInput
		want error
	}{
		{"unknown source", app.InstallInput{Source: "nope", Ref: "x"}, core.ErrInvalid},
		{"command source of a marketplace", app.InstallInput{Source: "claude-code", Ref: "/does/not/exist"}, nil},
		{"empty ref", app.InstallInput{Source: "agent-skills"}, core.ErrInvalid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			env := newPluginEnv(t)
			_, err := env.svc.Install(humanCtx(), tt.in)
			if err == nil || (tt.want != nil && !errors.Is(err, tt.want)) {
				t.Fatalf("error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestPlugins_EnableDisableUninstall(t *testing.T) {
	t.Parallel()
	env := newPluginEnv(t)
	p, err := env.svc.Install(humanCtx(), app.InstallInput{Source: "agent-skills", Ref: env.src})
	if err != nil {
		t.Fatal(err)
	}

	on, err := env.svc.Enable(humanCtx(), p.ID)
	if err != nil || !on.Enabled {
		t.Fatalf("Enable() = %+v, %v", on, err)
	}
	off, err := env.svc.Disable(humanCtx(), p.ID)
	if err != nil || off.Enabled {
		t.Fatalf("Disable() = %+v, %v", off, err)
	}
	if err := env.svc.Uninstall(humanCtx(), p.ID); err != nil {
		t.Fatalf("Uninstall() error = %v", err)
	}
	if _, err := env.svc.Get(humanCtx(), p.ID); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("Get after uninstall error = %v, want not found", err)
	}
	if _, err := os.Stat(p.Dir); !os.IsNotExist(err) {
		t.Errorf("plugin directory still there: %v", err)
	}
	if len(env.themes.removed) != 2 || env.themes.removed[1] != p.ID { // once on disable, once on uninstall
		t.Errorf("themes not told: %v", env.themes.removed)
	}
	actions := []string{}
	for _, e := range env.audit.Entries() {
		actions = append(actions, e.Action)
	}
	want := []string{"plugin.install", "plugin.enable", "plugin.disable", "plugin.uninstall"}
	if len(actions) != len(want) {
		t.Fatalf("audit actions = %v, want %v", actions, want)
	}
	for i := range want {
		if actions[i] != want[i] {
			t.Errorf("audit actions = %v, want %v", actions, want)
		}
	}
}

func TestPlugins_AnUnknownPluginIsNotFound(t *testing.T) {
	t.Parallel()
	env := newPluginEnv(t)
	id := "plg_00000000000000000000000000"
	if _, err := env.svc.Enable(humanCtx(), id); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("Enable error = %v", err)
	}
	if err := env.svc.Uninstall(humanCtx(), id); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("Uninstall error = %v", err)
	}
}

func TestPlugins_HealthFailsWhenTheInstalledTreeWasChanged(t *testing.T) {
	t.Parallel()
	env := newPluginEnv(t)
	p, err := env.svc.Install(humanCtx(), app.InstallInput{Source: "agent-skills", Ref: env.src})
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(p.Dir, "skills", "notes", "SKILL.md"), "tampered")

	if _, err := env.svc.Enable(humanCtx(), p.ID); !errors.Is(err, domain.ErrIntegrity) {
		t.Fatalf("Enable error = %v, want ErrIntegrity", err)
	}
}

func TestPlugins_PanelTokenUsesTheManifestScopes(t *testing.T) {
	t.Parallel()
	env := newPluginEnv(t)
	p, err := env.svc.Install(humanCtx(), app.InstallInput{Source: "agent-skills", Ref: env.src, Enable: true})
	if err != nil {
		t.Fatal(err)
	}

	// A skills plugin has no panel, so no token can be issued for it.
	if _, err := env.svc.IssuePanelToken(humanCtx(), p.ID, time.Hour); !errors.Is(err, core.ErrConflict) {
		t.Fatalf("IssuePanelToken error = %v, want conflict", err)
	}
}

func TestPlugins_RunnersListBuiltinsAndEnabledPluginRunners(t *testing.T) {
	t.Parallel()
	env := newPluginEnv(t)
	p := plugin("acme-runner", "1.0.0", func(m *domain.Manifest) {
		m.Contents.Runners = []domain.Runner{{Name: "acme", Command: "${PLUGIN_ROOT}/bin/acme", ProtocolVersions: []string{"1.0"}, Description: "Acme."}}
	})
	p.Dir = "/plugins/acme-runner/1.0.0"
	if err := env.repo.Insert(context.Background(), p); err != nil {
		t.Fatal(err)
	}

	infos, err := env.svc.Runners(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, r := range infos {
		names = append(names, r.Name)
	}
	if len(names) != 3 || names[0] != "acme" || names[1] != "echo" || names[2] != "openai" {
		t.Fatalf("runners = %v", names)
	}

	ad, err := env.svc.Runner(context.Background(), "acme")
	if err != nil {
		t.Fatal(err)
	}
	spec, err := ad.Exec(context.Background(), core.Agent{Name: "ada"}, "/run/ada.sock")
	if err != nil {
		t.Fatal(err)
	}
	if spec.Path != "/plugins/acme-runner/1.0.0/bin/acme" {
		t.Errorf("spec = %+v", spec)
	}
	if _, err := env.svc.Runner(context.Background(), "ghost"); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("unknown runner error = %v", err)
	}

	echo, _ := env.svc.Runner(context.Background(), "echo")
	if spec, _ := echo.Exec(context.Background(), core.Agent{Name: "ada"}, "/s"); spec.Path != "/usr/lib/agentsone/runners/echo" {
		t.Errorf("echo spec = %+v", spec)
	}
}
