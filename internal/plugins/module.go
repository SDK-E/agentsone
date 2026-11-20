// SPDX-License-Identifier: MIT

package plugins

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/SDK-E/agentsone/internal/contract/api/pluginsapi"
	"github.com/SDK-E/agentsone/internal/contract/api/runnersapi"
	"github.com/SDK-E/agentsone/internal/contract/api/themesapi"
	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/plugins/adapters/fetch"
	"github.com/SDK-E/agentsone/internal/plugins/adapters/fsinstall"
	httpadapter "github.com/SDK-E/agentsone/internal/plugins/adapters/http"
	"github.com/SDK-E/agentsone/internal/plugins/adapters/sqlite"
	"github.com/SDK-E/agentsone/internal/plugins/app"
	"github.com/SDK-E/agentsone/internal/plugins/domain"
	"github.com/SDK-E/agentsone/internal/store"
)

// defaultThemePlugin is the bundled plugin whose theme is the built-in theme.
const defaultThemePlugin = "agents-one-theme"

// Config is the [plugins] table of the daemon config. Every key is optional.
type Config struct {
	// Sources replaces the default catalog sources when set.
	Sources []SourceConfig `toml:"sources"`
	// BundledDir is where the image keeps the default plugins. It defaults
	// to <lib_dir>/plugins.
	BundledDir string `toml:"bundled_dir"`
}

// SourceConfig is one catalog source.
type SourceConfig struct {
	ID      string `toml:"id"`
	Name    string `toml:"name"`
	URL     string `toml:"url"`
	Enabled bool   `toml:"enabled"`
}

// DefaultSources are the four catalog sources a fresh install knows.
func DefaultSources() []SourceConfig {
	return []SourceConfig{
		{ID: "claude-code", Name: "Claude Code plugins", URL: "https://github.com/anthropics/claude-plugins-official", Enabled: true},
		{ID: "agentsone", Name: "Agents One plugins", URL: "https://github.com/SDK-E/plugins", Enabled: true},
		{ID: "agent-skills", Name: "Agent Skills", URL: "https://github.com/anthropics/skills", Enabled: true},
		{ID: "mcp-registry", Name: "MCP registry", URL: "https://registry.modelcontextprotocol.io", Enabled: true},
	}
}

// Module wires the plugin context into the daemon.
type Module struct {
	core.Base
	deps    core.Deps
	db      *store.DB
	cfg     Config
	plugins *app.Plugins
	themes  *app.Themes
}

var _ core.Module = (*Module)(nil)

// New builds the plugins module on deps.Stores.Core. A bad [plugins] table
// falls back to the defaults and is logged.
func New(deps core.Deps) *Module {
	m := &Module{deps: deps, db: deps.Stores.Core, cfg: Config{Sources: DefaultSources()}}
	if deps.Config != nil {
		if err := deps.Config.Section("plugins", &m.cfg); err != nil {
			deps.Log.Error("plugins: bad config, using defaults", "error", err)
			m.cfg = Config{Sources: DefaultSources()}
		}
	}
	libDir, pluginsDir := core.LibDir, filepath.Join(core.StateDir, "plugins")
	if deps.Config != nil {
		libDir, pluginsDir = deps.Config.LibDir, deps.Config.PluginsDir()
	}
	if m.cfg.BundledDir == "" {
		m.cfg.BundledDir = filepath.Join(libDir, "plugins")
	}
	ports := lazyPorts{deps.Ports}
	m.themes = app.NewThemes(sqlite.NewThemes(m.db), ports, deps.Clock, deps.IDs)
	_, podman := exec.LookPath("podman")
	sources := make([]domain.SourceInfo, 0, len(m.cfg.Sources))
	for _, s := range m.cfg.Sources {
		sources = append(sources, domain.SourceInfo{ID: s.ID, Name: s.Name, URL: s.URL, Enabled: s.Enabled})
	}
	m.plugins = app.NewPlugins(app.PluginsDeps{
		Repo: sqlite.NewPlugins(m.db), Fetcher: fetch.New(), Installer: fsinstall.New(pluginsDir, domain.DefaultInstallLimits),
		Audit: ports, Clock: deps.Clock, IDs: deps.IDs, Themes: m.themes, Tokens: ports,
		Sources: sources, LibDir: libDir, ContainerEngine: podman == nil, Log: deps.Log,
	})
	return m
}

// lazyPorts reads the audit and token ports when they are used, because
// other modules fill them during the Provide step, after New.
type lazyPorts struct{ p *core.Ports }

func (l lazyPorts) Append(ctx context.Context, e core.AuditEntry) error {
	return l.p.Audit.Append(ctx, e)
}

func (l lazyPorts) Issue(ctx context.Context, subject core.Caller, scopes []string, ttl time.Duration) (core.Token, error) {
	return l.p.Tokens.Issue(ctx, subject, scopes, ttl)
}

func (l lazyPorts) Verify(ctx context.Context, value string) (core.Caller, error) {
	return l.p.Tokens.Verify(ctx, value)
}

// Name is the module and migration namespace.
func (*Module) Name() string { return "plugins" }

// Provide makes the plugin manager the daemon's RunnerRegistry and
// TemplateCatalog.
func (m *Module) Provide(p *core.Ports) {
	p.Runners = m.plugins
	p.Templates = m.plugins.TemplateCatalog()
}

// Migrate creates the plugin and theme tables.
func (m *Module) Migrate(ctx context.Context) error { return sqlite.Migrate(ctx, m.db) }

// Start installs the bundled default plugins, once. A failure is logged and
// does not stop the daemon: it can run without its default plugins.
func (m *Module) Start(ctx context.Context) error {
	installed, err := m.plugins.Bootstrap(ctx, m.cfg.BundledDir, app.BootstrapOptions{
		DefaultThemePlugin: defaultThemePlugin,
		EnsureBuiltinTheme: func(ctx context.Context, raw []byte) error {
			_, err := m.themes.EnsureBuiltin(ctx, raw)
			return err
		},
	})
	if err != nil {
		m.deps.Log.Error("plugins: bootstrap of bundled plugins failed", "error", err)
		return nil
	}
	if len(installed) > 0 {
		m.deps.Log.Info("plugins: installed bundled plugins", "plugins", installed)
	}
	return nil
}

// API serves the plugins, runners and themes tags.
func (m *Module) API() core.API {
	return core.API{Groups: []core.APIGroup{
		pluginsapi.Group(httpadapter.NewPluginsHandler(pluginService{m.plugins})),
		runnersapi.Group(httpadapter.NewRunnersHandler(runnerService{m.plugins})),
		themesapi.Group(httpadapter.NewThemesHandler(m.themes)),
	}}
}

// pluginService adapts the use cases to the HTTP handler's port.
type pluginService struct{ *app.Plugins }

func (s pluginService) Install(ctx context.Context, in httpadapter.InstallInput) (domain.Plugin, error) {
	return s.Plugins.Install(ctx, app.InstallInput{Source: in.Source, Ref: in.Ref, Version: in.Version, Enable: in.Enable})
}

func (s pluginService) IssuePanelToken(ctx context.Context, id string, ttl time.Duration) (httpadapter.PanelToken, error) {
	t, err := s.Plugins.IssuePanelToken(ctx, id, ttl)
	if err != nil {
		return httpadapter.PanelToken{}, fmt.Errorf("panel token: %w", err)
	}
	return httpadapter.PanelToken{
		ID: t.ID, Value: t.Value, Label: t.Label, Subject: t.Subject, Scopes: t.Scopes, CreatedAt: t.CreatedAt, ExpiresAt: t.ExpiresAt,
	}, nil
}

// runnerService adapts the use cases to the runners handler's port.
type runnerService struct{ p *app.Plugins }

func (s runnerService) List(ctx context.Context) ([]domain.RunnerEntry, error) {
	return s.p.RunnerEntries(ctx)
}

func (s runnerService) Get(ctx context.Context, name string) (domain.RunnerEntry, error) {
	return s.p.RunnerEntry(ctx, name)
}
