// SPDX-License-Identifier: MIT

package app_test

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/plugins/adapters/fetch"
	"github.com/SDK-E/agentsone/internal/plugins/adapters/memory"
	"github.com/SDK-E/agentsone/internal/plugins/app"
	"github.com/SDK-E/agentsone/internal/plugins/domain"
	"github.com/SDK-E/agentsone/internal/plugins/pluginstest"
)

// errInjected is the failure the fakes below hand back.
var errInjected = errors.New("injected failure")

// faults says which collaborator call fails. The fakes read it at call time,
// so a test seeds a plugin first and breaks the collaborator afterwards.
type faults struct {
	// repository
	byName, insert, list, setEnabled, del error
	listLimit                             int
	// installer
	install, verify, remove error
	verifySum               string // when set, Verify answers this digest
	removed                 []string
	// theme hook
	importTheme, removeTheme error
	// audit log
	audit error
}

type faultRepo struct {
	*memory.Plugins
	f *faults
}

func (r faultRepo) Insert(ctx context.Context, p domain.Plugin) error {
	if r.f.insert != nil {
		return r.f.insert
	}
	return r.Plugins.Insert(ctx, p)
}

func (r faultRepo) ByName(ctx context.Context, name string) ([]domain.Plugin, error) {
	if r.f.byName != nil {
		return nil, r.f.byName
	}
	return r.Plugins.ByName(ctx, name)
}

func (r faultRepo) List(ctx context.Context, f domain.PluginFilter, after string, limit int) ([]domain.Plugin, error) {
	r.f.listLimit = limit
	if r.f.list != nil {
		return nil, r.f.list
	}
	return r.Plugins.List(ctx, f, after, limit)
}

func (r faultRepo) SetEnabled(ctx context.Context, id string, enabled bool, h domain.Health) error {
	if r.f.setEnabled != nil {
		return r.f.setEnabled
	}
	return r.Plugins.SetEnabled(ctx, id, enabled, h)
}

func (r faultRepo) Delete(ctx context.Context, id string) error {
	if r.f.del != nil {
		return r.f.del
	}
	return r.Plugins.Delete(ctx, id)
}

type faultInstaller struct {
	domain.Installer
	f *faults
}

func (i faultInstaller) Install(ctx context.Context, req domain.InstallRequest) (domain.InstallResult, error) {
	if i.f.install != nil {
		return domain.InstallResult{}, i.f.install
	}
	return i.Installer.Install(ctx, req)
}

func (i faultInstaller) Verify(dir string) (string, error) {
	if i.f.verify != nil {
		return "", i.f.verify
	}
	if i.f.verifySum != "" {
		return i.f.verifySum, nil
	}
	return i.Installer.Verify(dir)
}

func (i faultInstaller) Remove(name, version string) error {
	i.f.removed = append(i.f.removed, name+"@"+version)
	if i.f.remove != nil {
		return i.f.remove
	}
	return i.Installer.Remove(name, version)
}

type faultThemes struct {
	*fakeThemeHook
	f *faults
}

func (t faultThemes) ImportFromPlugin(ctx context.Context, id string, raw []byte) (domain.InstalledTheme, error) {
	if t.f.importTheme != nil {
		return domain.InstalledTheme{}, t.f.importTheme
	}
	return t.fakeThemeHook.ImportFromPlugin(ctx, id, raw)
}

func (t faultThemes) RemoveFromPlugin(ctx context.Context, id string) error {
	if t.f.removeTheme != nil {
		return t.f.removeTheme
	}
	return t.fakeThemeHook.RemoveFromPlugin(ctx, id)
}

type faultAudit struct{ f *faults }

func (a faultAudit) Append(context.Context, core.AuditEntry) error { return a.f.audit }

// newFaulty builds the plugin manager over fakes that fail on demand.
func newFaulty(t *testing.T, fetcher domain.Fetcher) (*app.Plugins, *faults, *memory.Plugins) {
	t.Helper()
	f := &faults{}
	mem := memory.NewPlugins()
	svc, _, _ := depsWith(t, func(d *app.PluginsDeps) {
		themes, _ := d.Themes.(*fakeThemeHook)
		d.Repo = faultRepo{mem, f}
		d.Installer = faultInstaller{d.Installer, f}
		d.Themes = faultThemes{themes, f}
		d.Audit = faultAudit{f}
		d.Fetcher = fetcher
	})
	return svc, f, mem
}

func brightInput(enable bool) app.InstallInput {
	return app.InstallInput{Source: "agentsone", Ref: "acme/bright", Enable: enable}
}

func seedBright(t *testing.T, svc *app.Plugins) domain.Plugin {
	t.Helper()
	p, err := svc.Install(humanCtx(), brightInput(false))
	if err != nil {
		t.Fatalf("seed Install() error = %v", err)
	}
	return p
}

func storedPlugins(t *testing.T, mem *memory.Plugins) []domain.Plugin {
	t.Helper()
	list, err := mem.List(context.Background(), domain.PluginFilter{}, "", 50)
	if err != nil {
		t.Fatal(err)
	}
	return list
}

func versionedTree(version string) fstest.MapFS {
	tree := nativeTree()
	raw := string(tree["agentsone.plugin.json"].Data)
	tree["agentsone.plugin.json"] = &fstest.MapFile{Data: []byte(strings.Replace(raw, `"version": "1.0.0"`, `"version": "`+version+`"`, 1))}
	return tree
}

func skillsTree() fstest.MapFS {
	return fstest.MapFS{
		"skills/notes/SKILL.md": {Data: []byte("---\nname: notes\ndescription: Take notes.\n---\nBody")},
		"skills/todo/SKILL.md":  {Data: []byte("---\nname: todo\ndescription: Keep a todo list.\n---\nBody")},
	}
}

func TestPluginsExtra_LifecycleFailures(t *testing.T) {
	t.Parallel()
	enable := func(svc *app.Plugins, id string) error { _, err := svc.Enable(humanCtx(), id); return err }
	disable := func(svc *app.Plugins, id string) error { _, err := svc.Disable(humanCtx(), id); return err }
	uninstall := func(svc *app.Plugins, id string) error { return svc.Uninstall(humanCtx(), id) }
	tests := []struct {
		name    string
		op      func(*app.Plugins, string) error
		arrange func(*faults)
		id      string // empty: the seeded plugin
		want    error
		wantMsg string
	}{
		{name: "enable: unknown plugin", op: enable, id: "plg_missing", want: core.ErrNotFound},
		{name: "enable: verify fails", op: enable, arrange: func(f *faults) { f.verify = errInjected }, want: errInjected, wantMsg: "enable plugin"},
		{name: "enable: theme import fails", op: enable, arrange: func(f *faults) { f.importTheme = errInjected }, want: errInjected},
		{name: "enable: store fails", op: enable, arrange: func(f *faults) { f.setEnabled = errInjected }, want: errInjected, wantMsg: "enable plugin"},
		{name: "enable: audit fails", op: enable, arrange: func(f *faults) { f.audit = errInjected }, want: errInjected, wantMsg: "record audit"},
		{name: "disable: unknown plugin", op: disable, id: "plg_missing", want: core.ErrNotFound},
		{name: "disable: theme removal fails", op: disable, arrange: func(f *faults) { f.removeTheme = errInjected }, want: errInjected},
		{name: "disable: store fails", op: disable, arrange: func(f *faults) { f.setEnabled = errInjected }, want: errInjected, wantMsg: "disable plugin"},
		{name: "disable: audit fails", op: disable, arrange: func(f *faults) { f.audit = errInjected }, want: errInjected, wantMsg: "record audit"},
		{name: "uninstall: unknown plugin", op: uninstall, id: "plg_missing", want: core.ErrNotFound},
		{name: "uninstall: theme removal fails", op: uninstall, arrange: func(f *faults) { f.removeTheme = errInjected }, want: errInjected},
		{name: "uninstall: store fails", op: uninstall, arrange: func(f *faults) { f.del = errInjected }, want: errInjected, wantMsg: "uninstall plugin"},
		{name: "uninstall: installer fails", op: uninstall, arrange: func(f *faults) { f.remove = errInjected }, want: errInjected, wantMsg: "uninstall plugin"},
		{name: "uninstall: audit fails", op: uninstall, arrange: func(f *faults) { f.audit = errInjected }, want: errInjected, wantMsg: "record audit"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			svc, f, _ := newFaulty(t, &specFetcher{files: nativeTree()})
			p := seedBright(t, svc)
			if tt.arrange != nil {
				tt.arrange(f)
			}
			id := tt.id
			if id == "" {
				id = p.ID
			}

			err := tt.op(svc, id)

			if err == nil || !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want %v", err, tt.want)
			}
			if tt.wantMsg != "" && !strings.Contains(err.Error(), tt.wantMsg) {
				t.Errorf("error %q lacks %q", err, tt.wantMsg)
			}
		})
	}
}

func TestPluginsExtra_EnableRefusesAThemeFileItCannotRead(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		theme string
		want  error
	}{
		{"file is missing", "./themes/missing.json", fs.ErrNotExist},
		{"path leaves the plugin", "../outside.json", core.ErrInvalid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			svc, f, mem := newFaulty(t, &specFetcher{})
			p := plugin("themed", "1.0.0", func(m *domain.Manifest) { m.Contents.Themes = []domain.FileRef{{Path: tt.theme}} })
			p.Enabled, p.Dir, p.SHA256 = false, t.TempDir(), "sum"
			if err := mem.Insert(context.Background(), p); err != nil {
				t.Fatal(err)
			}
			f.verifySum = "sum"

			_, err := svc.Enable(humanCtx(), p.ID)

			if !errors.Is(err, tt.want) {
				t.Fatalf("Enable() error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestPluginsExtra_InstallFailuresLeaveNothingBehind(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		arrange     func(*faults)
		enable      bool
		want        error
		wantMsg     string
		wantRemoved int // calls to Installer.Remove
	}{
		{name: "store lookup fails", arrange: func(f *faults) { f.byName = errInjected }, want: errInjected},
		{name: "installer fails", arrange: func(f *faults) { f.install = errInjected }, want: errInjected},
		{name: "store insert fails and the files are removed", arrange: func(f *faults) { f.insert = errInjected }, want: errInjected, wantRemoved: 1},
		{name: "enable fails and the install is rolled back", arrange: func(f *faults) { f.verify = errInjected }, enable: true, want: errInjected, wantRemoved: 1},
		{name: "theme import fails and the install is rolled back", arrange: func(f *faults) { f.importTheme = errInjected }, enable: true, want: errInjected, wantRemoved: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			svc, f, mem := newFaulty(t, &specFetcher{files: nativeTree()})
			tt.arrange(f)

			_, err := svc.Install(humanCtx(), brightInput(tt.enable))

			if !errors.Is(err, tt.want) {
				t.Fatalf("Install() error = %v, want %v", err, tt.want)
			}
			if got := storedPlugins(t, mem); len(got) != 0 {
				t.Errorf("repository holds %+v, want it untouched", got)
			}
			if len(f.removed) != tt.wantRemoved {
				t.Errorf("Installer.Remove calls = %v, want %d", f.removed, tt.wantRemoved)
			}
		})
	}
}

func TestPluginsExtra_InstallAuditFailureIsReported(t *testing.T) {
	t.Parallel()
	svc, f, _ := newFaulty(t, &specFetcher{files: nativeTree()})
	f.audit = errInjected

	_, err := svc.Install(humanCtx(), brightInput(false))

	if !errors.Is(err, errInjected) || !strings.Contains(err.Error(), "plugin.install: record audit") {
		t.Fatalf("Install() error = %v", err)
	}
}

func TestPluginsExtra_AnotherVersionOfAnInstalledNameIsAllowedButNotTheSameOne(t *testing.T) {
	t.Parallel()
	fetcher := &specFetcher{files: versionedTree("1.0.0")}
	svc, _, mem := newFaulty(t, fetcher)
	seedBright(t, svc)

	fetcher.files = versionedTree("2.0.0")
	if _, err := svc.Install(humanCtx(), brightInput(false)); err != nil {
		t.Fatalf("second version: Install() error = %v", err)
	}
	if got := storedPlugins(t, mem); len(got) != 2 {
		t.Fatalf("stored = %d plugins, want both versions", len(got))
	}

	_, err := svc.Install(humanCtx(), brightInput(false))

	if !errors.Is(err, core.ErrConflict) || !errors.Is(err, domain.ErrAlreadyInstalled) {
		t.Fatalf("same version again: error = %v, want a conflict", err)
	}
}

func TestPluginsExtra_ListClampsTheLimitAndReportsStoreErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		limit     int
		wantAsked int // the repository is asked for one more than the page
	}{
		{"zero uses the default", 0, 51},
		{"negative uses the default", -5, 51},
		{"small is kept", 3, 4},
		{"the maximum is kept", 200, 201},
		{"over the maximum is clamped", 999, 201},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			svc, f, _ := newFaulty(t, &specFetcher{})

			got, next, err := svc.List(humanCtx(), domain.PluginFilter{}, "", tt.limit)

			if err != nil || got == nil || len(got) != 0 || next != "" {
				t.Fatalf("List() = %v, %q, %v, want an empty page", got, next, err)
			}
			if f.listLimit != tt.wantAsked {
				t.Errorf("repository asked for %d, want %d", f.listLimit, tt.wantAsked)
			}
		})
	}

	t.Run("a store error is reported", func(t *testing.T) {
		t.Parallel()
		svc, f, _ := newFaulty(t, &specFetcher{})
		f.list = errInjected

		_, _, err := svc.List(humanCtx(), domain.PluginFilter{}, "", 10)

		if !errors.Is(err, errInjected) || !strings.Contains(err.Error(), "list plugins") {
			t.Fatalf("List() error = %v", err)
		}
	})
}

func TestPluginsExtra_EveryReaderOfAllPluginsReportsStoreErrors(t *testing.T) {
	t.Parallel()
	ctx := humanCtx()
	tests := []struct {
		name string
		call func(*app.Plugins) error
	}{
		{"catalog", func(s *app.Plugins) error { _, _, err := s.Catalog(ctx, domain.CatalogQuery{}); return err }},
		{"runner entries", func(s *app.Plugins) error { _, err := s.RunnerEntries(ctx); return err }},
		{"runner entry", func(s *app.Plugins) error { _, err := s.RunnerEntry(ctx, "echo"); return err }},
		{"runners", func(s *app.Plugins) error { _, err := s.Runners(ctx); return err }},
		{"runner", func(s *app.Plugins) error { _, err := s.Runner(ctx, "not-a-builtin"); return err }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			svc, f, _ := newFaulty(t, &specFetcher{})
			f.list = errInjected

			if err := tt.call(svc); !errors.Is(err, errInjected) {
				t.Fatalf("error = %v, want the store error", err)
			}
		})
	}
}

// listRepo serves a fixed, sorted list and counts the pages asked for.
type listRepo struct {
	domain.PluginRepository
	plugins []domain.Plugin
	calls   int
}

func (r *listRepo) List(_ context.Context, _ domain.PluginFilter, after string, limit int) ([]domain.Plugin, error) {
	r.calls++
	var out []domain.Plugin
	for _, p := range r.plugins {
		if p.ID > after && len(out) < limit {
			out = append(out, p)
		}
	}
	return out, nil
}

func TestPluginsExtra_RunnersComeFromEveryPageOfEnabledPlugins(t *testing.T) {
	t.Parallel()
	const total = 450 // three pages of 200, 200 and 50
	repo := &listRepo{}
	for i := 1; i <= total; i++ {
		p := pluginstest.Plugin(i)
		p.Enabled = false
		switch i {
		case 10: // disabled: its runner is not offered
			p.Manifest.Contents.Runners = []domain.Runner{{Name: "off", Command: "bin/off"}}
		case 20: // a plugin never replaces a built-in runner
			p.Enabled = true
			p.Manifest.Contents.Runners = []domain.Runner{{Name: "echo", Command: "bin/echo"}}
		case 300: // no protocol versions: the default
			p.Enabled = true
			p.Manifest.Contents.Runners = []domain.Runner{{Name: "plain", Command: "bin/plain"}}
		case total: // on the last page: the newest protocol version
			p.Enabled = true
			p.Manifest.Contents.Runners = []domain.Runner{{Name: "late", Command: "bin/late", ProtocolVersions: []string{"1.0", "1.1"}}}
		}
		repo.plugins = append(repo.plugins, p)
	}
	svc, _, _ := depsWith(t, func(d *app.PluginsDeps) { d.Repo = repo })
	ctx := humanCtx()

	entries, err := svc.RunnerEntries(ctx)
	if err != nil {
		t.Fatalf("RunnerEntries() error = %v", err)
	}
	if repo.calls != 3 {
		t.Errorf("repository pages = %d, want 3", repo.calls)
	}
	got := map[string]domain.RunnerEntry{}
	for _, e := range entries {
		got[e.Name] = e
	}
	if _, ok := got["off"]; ok {
		t.Error("the runner of a disabled plugin is offered")
	}
	if e := got["echo"]; !e.Builtin || e.PluginID != "" {
		t.Errorf("echo = %+v, want the built-in runner", e)
	}
	if e := got["plain"]; e.ProtocolVersion != "1.0" || e.PluginID != pluginstest.Plugin(300).ID {
		t.Errorf("plain = %+v, want the default protocol version", e)
	}
	if e := got["late"]; e.ProtocolVersion != "1.1" || e.PluginID != pluginstest.Plugin(total).ID {
		t.Errorf("late = %+v, want the last protocol version of the last page", e)
	}

	if _, err := svc.Runner(ctx, "late"); err != nil {
		t.Errorf("Runner(late) error = %v", err)
	}
	for _, name := range []string{"off", "unknown"} {
		if _, err := svc.Runner(ctx, name); !errors.Is(err, core.ErrNotFound) {
			t.Errorf("Runner(%s) error = %v, want not found", name, err)
		}
	}
}

func TestPluginsExtra_TheNameOfASkillPluginComesFromTheRepoName(t *testing.T) {
	t.Parallel()
	tests := []struct {
		ref  string
		want string
	}{
		{"https://example.com/acme/team-skills.git", "team-skills"},
		{"https://example.com/acme/team-skills/", "team-skills"},
		{"https://example.com/acme/team-skills.git/", "team-skills"},
		{"acme/pack-skills", "pack-skills"},
		{".", "skills"},
		{"/", "skills"},
	}
	for _, tt := range tests {
		t.Run(tt.ref, func(t *testing.T) {
			t.Parallel()
			svc, _, _ := depsWith(t, func(d *app.PluginsDeps) { d.Fetcher = &specFetcher{files: skillsTree()} })

			p, err := svc.Install(humanCtx(), app.InstallInput{Source: "agent-skills", Ref: tt.ref})

			if err != nil || p.Name != tt.want {
				t.Fatalf("Install() = %q, %v, want the name %q", p.Name, err, tt.want)
			}
		})
	}
}

func TestPluginsExtra_FetchErrorsMapToTheirCoreSentinels(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		err        error
		wantIs     []error
		wantNot    []error
		wantStatus int // problem status, when it carries one
	}{
		{"too large is a 413 problem", domain.ErrTooLarge, []error{domain.ErrTooLarge}, []error{core.ErrInvalid, core.ErrConflict}, 413},
		{"integrity is invalid", domain.ErrIntegrity, []error{core.ErrInvalid, domain.ErrIntegrity}, []error{core.ErrConflict}, 0},
		{"incompatible is invalid", domain.ErrIncompatible, []error{core.ErrInvalid}, nil, 0},
		{"not found is not found", domain.ErrNotFound, []error{core.ErrNotFound}, []error{core.ErrInvalid}, 0},
		{"in use is a conflict", domain.ErrInUse, []error{core.ErrConflict}, nil, 0},
		{"built in is a conflict", domain.ErrBuiltin, []error{core.ErrConflict}, nil, 0},
		{"other errors pass through", errInjected, []error{errInjected}, []error{core.ErrInvalid, core.ErrConflict, core.ErrNotFound}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			svc, _, _ := depsWith(t, func(d *app.PluginsDeps) { d.Fetcher = &specFetcher{err: fmt.Errorf("fetch: %w", tt.err)} })

			_, err := svc.Install(humanCtx(), app.InstallInput{Source: "agent-skills", Ref: "acme/skills"})

			if err == nil {
				t.Fatal("Install() succeeded")
			}
			for _, want := range tt.wantIs {
				if !errors.Is(err, want) {
					t.Errorf("error %q is not %v", err, want)
				}
			}
			for _, not := range tt.wantNot {
				if errors.Is(err, not) {
					t.Errorf("error %q is %v", err, not)
				}
			}
			if tt.wantStatus != 0 {
				if p := core.ProblemFrom(err); p.Status != tt.wantStatus {
					t.Errorf("problem status = %d, want %d", p.Status, tt.wantStatus)
				}
			}
		})
	}
}

func TestPluginsExtra_BootstrapFailures(t *testing.T) {
	t.Parallel()
	writeBundledPlugin := func(t *testing.T, dir, name string) {
		t.Helper()
		writeFile(t, filepath.Join(dir, name, "agentsone.plugin.json"), bundledManifest(name))
		writeFile(t, filepath.Join(dir, name, "skills", "seed", "SKILL.md"), "---\nname: seed\ndescription: Seed.\n---\nx")
	}
	writeTheme := func(t *testing.T, dir string, withFile bool) {
		t.Helper()
		manifest := strings.Replace(bundledManifest("agents-one-theme"), `"themes": []`, `"themes": [{"path": "./themes/theme.json"}]`, 1)
		writeFile(t, filepath.Join(dir, "agents-one-theme", "agentsone.plugin.json"), manifest)
		writeFile(t, filepath.Join(dir, "agents-one-theme", "skills", "seed", "SKILL.md"), "---\nname: seed\ndescription: Seed.\n---\nx")
		if withFile {
			writeFile(t, filepath.Join(dir, "agents-one-theme", "themes", "theme.json"), `{"name":"Agents One Theme"}`)
		}
	}
	themeOpt := func(hook func(context.Context, []byte) error) app.BootstrapOptions {
		return app.BootstrapOptions{DefaultThemePlugin: "agents-one-theme", EnsureBuiltinTheme: hook}
	}
	tests := []struct {
		name      string
		prepare   func(t *testing.T, dir string) string // returns the dir to bootstrap
		mutate    func(t *testing.T, d *app.PluginsDeps, f *faults)
		opt       app.BootstrapOptions
		want      error
		wantMsg   string
		wantNames []string
	}{
		{
			name: "a manifest that does not parse",
			prepare: func(t *testing.T, dir string) string {
				writeFile(t, filepath.Join(dir, "broken", "agentsone.plugin.json"), "{not json")
				return dir
			},
			wantMsg: "bootstrap plugins: broken",
		},
		{
			name: "the folder is a file",
			prepare: func(t *testing.T, dir string) string {
				path := filepath.Join(dir, "file")
				writeFile(t, path, "x")
				return path
			},
			wantMsg: "bootstrap plugins: read",
		},
		{
			name: "the install fails",
			prepare: func(t *testing.T, dir string) string {
				writeBundledPlugin(t, dir, "small-seed")
				return dir
			},
			mutate:  func(_ *testing.T, d *app.PluginsDeps, _ *faults) { d.Fetcher = &specFetcher{err: errInjected} },
			want:    errInjected,
			wantMsg: "bootstrap plugins: install small-seed",
		},
		{
			name: "the store lookup fails",
			prepare: func(t *testing.T, dir string) string {
				writeBundledPlugin(t, dir, "small-seed")
				return dir
			},
			mutate:  func(_ *testing.T, _ *app.PluginsDeps, f *faults) { f.byName = errInjected },
			want:    errInjected,
			wantMsg: "bootstrap plugins",
		},
		{
			name: "the built-in theme hook fails",
			prepare: func(t *testing.T, dir string) string {
				writeTheme(t, dir, true)
				return dir
			},
			opt:       themeOpt(func(context.Context, []byte) error { return errInjected }),
			want:      errInjected,
			wantMsg:   "bootstrap plugins: built-in theme",
			wantNames: []string{"agents-one-theme"},
		},
		{
			name: "the theme file is missing",
			prepare: func(t *testing.T, dir string) string {
				writeTheme(t, dir, false)
				return dir
			},
			opt:       themeOpt(func(context.Context, []byte) error { return nil }),
			want:      fs.ErrNotExist,
			wantNames: []string{"agents-one-theme"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dir := tt.prepare(t, t.TempDir())
			f := &faults{}
			svc, _, _ := depsWith(t, func(d *app.PluginsDeps) {
				d.Fetcher = fetch.New(fetch.WithTempDir(t.TempDir()))
				d.Repo = faultRepo{memory.NewPlugins(), f}
				if tt.mutate != nil {
					tt.mutate(t, d, f)
				}
			})

			got, err := svc.Bootstrap(context.Background(), dir, tt.opt)

			if err == nil {
				t.Fatalf("Bootstrap() = %v, want an error", got)
			}
			if tt.want != nil && !errors.Is(err, tt.want) {
				t.Errorf("error = %v, want %v", err, tt.want)
			}
			if tt.wantMsg != "" && !strings.Contains(err.Error(), tt.wantMsg) {
				t.Errorf("error %q lacks %q", err, tt.wantMsg)
			}
			if strings.Join(got, ",") != strings.Join(tt.wantNames, ",") {
				t.Errorf("installed = %v, want %v", got, tt.wantNames)
			}
		})
	}
}

func TestPluginsExtra_BootstrapSkipsFoldersWithoutAManifest(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "empty-folder", "README.md"), "no manifest")
	svc, _, _ := depsWith(t, func(d *app.PluginsDeps) { d.Fetcher = &specFetcher{err: errInjected} })

	got, err := svc.Bootstrap(context.Background(), dir, app.BootstrapOptions{})

	if err != nil || len(got) != 0 {
		t.Fatalf("Bootstrap() = %v, %v, want nothing and no error", got, err)
	}
}
