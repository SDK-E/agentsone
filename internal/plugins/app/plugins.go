// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/plugins/domain"
)

const (
	pluginIDPrefix   = "plg"
	pluginDefault    = 50
	pluginMax        = 200
	maxRef           = 500
	nativeManifest   = "agentsone.plugin.json"
	panelDefaultTTL  = time.Hour
	pluginSystemName = "system:plugins"
)

// ThemeHook registers and removes the themes a plugin carries. *Themes
// implements it.
type ThemeHook interface {
	ImportFromPlugin(ctx context.Context, pluginID string, raw []byte) (domain.InstalledTheme, error)
	RemoveFromPlugin(ctx context.Context, pluginID string) error
}

// CatalogProvider lists the installable plugins of one source.
type CatalogProvider interface {
	Search(ctx context.Context, q domain.CatalogQuery) ([]domain.CatalogEntry, error)
}

// RegistryLookup reads one server.json from the MCP registry.
type RegistryLookup interface {
	Server(ctx context.Context, name, version string) (domain.RegistryServer, error)
}

// PluginsDeps are what the plugin use cases need.
type PluginsDeps struct {
	Repo      domain.PluginRepository
	Fetcher   domain.Fetcher
	Installer domain.Installer
	Audit     core.AuditLog
	Clock     core.Clock
	IDs       core.IDs
	Themes    ThemeHook
	Tokens    core.TokenIssuer
	Registry  RegistryLookup // nil: the mcp-registry source is unavailable
	Catalogs  map[string]CatalogProvider
	Sources   []domain.SourceInfo
	// LibDir is where the image keeps built-in runners and plugins.
	LibDir string
	// ContainerEngine is true when podman is installed.
	ContainerEngine bool
}

// Plugins are the use cases of the plugins tag, and the RunnerRegistry.
type Plugins struct{ d PluginsDeps }

var _ core.RunnerRegistry = (*Plugins)(nil)

// NewPlugins returns the plugin manager.
func NewPlugins(d PluginsDeps) *Plugins { return &Plugins{d: d} }

// InstallInput is one install request.
type InstallInput struct {
	Source  string
	Ref     string
	Version string
	Enable  bool
}

// Sources lists the configured catalog sources.
func (p *Plugins) Sources(context.Context) ([]domain.SourceInfo, error) {
	return append([]domain.SourceInfo(nil), p.d.Sources...), nil
}

// Catalog searches the enabled sources and marks what is installed.
func (p *Plugins) Catalog(ctx context.Context, q domain.CatalogQuery) ([]domain.CatalogEntry, string, error) {
	var out []domain.CatalogEntry
	for _, s := range p.d.Sources {
		prov := p.d.Catalogs[s.ID]
		if !s.Enabled || prov == nil || (q.Source != "" && q.Source != s.ID) {
			continue
		}
		entries, err := prov.Search(ctx, q)
		if err != nil {
			return nil, "", fmt.Errorf("search %s: %w: %w", s.ID, core.ErrUnavailable, err)
		}
		out = append(out, entries...)
	}
	installed := map[string]bool{}
	all, err := p.all(ctx)
	if err != nil {
		return nil, "", err
	}
	for _, pl := range all {
		installed[pl.Name] = true
	}
	kept := out[:0]
	for _, e := range out {
		if q.Kind != "" && !contains(e.Kinds, q.Kind) {
			continue
		}
		e.Installed = installed[e.Name]
		kept = append(kept, e)
	}
	sort.Slice(kept, func(i, j int) bool { return kept[i].Ref < kept[j].Ref })
	return page(kept, q.After, q.Limit, func(e domain.CatalogEntry) string { return e.Ref })
}

// List returns up to limit plugins after the cursor.
func (p *Plugins) List(ctx context.Context, f domain.PluginFilter, after string, limit int) ([]domain.Plugin, string, error) {
	switch {
	case limit <= 0:
		limit = pluginDefault
	case limit > pluginMax:
		limit = pluginMax
	}
	items, err := p.d.Repo.List(ctx, f, after, limit+1)
	if err != nil {
		return nil, "", pluginError("list plugins", err)
	}
	next := ""
	if len(items) > limit {
		items = items[:limit]
		next = items[limit-1].ID
	}
	if items == nil {
		items = []domain.Plugin{}
	}
	return items, next, nil
}

// Get returns one plugin.
func (p *Plugins) Get(ctx context.Context, id string) (domain.Plugin, error) {
	pl, err := p.d.Repo.Get(ctx, id)
	if err != nil {
		return domain.Plugin{}, pluginError("get plugin", err)
	}
	return pl, nil
}

// Install fetches, normalises and installs a plugin, then enables it when
// asked. Agents may install, but not from a path on this machine.
func (p *Plugins) Install(ctx context.Context, in InstallInput) (domain.Plugin, error) {
	caller, ok := core.CallerFrom(ctx)
	if !ok {
		return domain.Plugin{}, fmt.Errorf("install plugin: %w", core.ErrUnauthorized)
	}
	if !validSource(in.Source) || in.Ref == "" || len(in.Ref) > maxRef {
		return domain.Plugin{}, fmt.Errorf("install plugin: source %q and a ref up to %d characters are required: %w", in.Source, maxRef, core.ErrInvalid)
	}
	norm, tree, err := p.fetchAndNormalize(ctx, caller, in)
	if err != nil {
		return domain.Plugin{}, pluginError("install plugin", err)
	}
	defer tree.Cleanup()

	m := norm.Manifest
	if _, err := p.findVersion(ctx, m.Name, m.Version); err == nil {
		return domain.Plugin{}, pluginError("install plugin", fmt.Errorf("%s %s: %w", m.Name, m.Version, domain.ErrAlreadyInstalled))
	} else if !errors.Is(err, domain.ErrNotFound) {
		return domain.Plugin{}, pluginError("install plugin", err)
	}
	raw, err := m.Encode()
	if err != nil {
		return domain.Plugin{}, pluginError("install plugin", err)
	}
	res, err := p.d.Installer.Install(ctx, domain.InstallRequest{
		Name: m.Name, Version: m.Version, Tree: tree.FS, Prefix: norm.TreePrefix, Extra: norm.Files, Manifest: raw,
	})
	if err != nil {
		return domain.Plugin{}, pluginError("install plugin", err)
	}
	rec := domain.Plugin{
		ID: p.d.IDs.New(pluginIDPrefix), Name: m.Name, Version: m.Version, Source: string(m.Source.Kind), Ref: m.Source.Ref,
		Health: domain.HealthOK, InstalledBy: domain.Actor{Kind: string(caller.Kind), Name: caller.Name},
		InstalledAt: p.d.Clock.Now().UTC(), Manifest: m, Dir: res.Dir, SHA256: res.SHA256,
	}
	for _, k := range m.Kinds() {
		rec.Kinds = append(rec.Kinds, string(k))
	}
	if err := p.d.Repo.Insert(ctx, rec); err != nil {
		_ = p.d.Installer.Remove(m.Name, m.Version)
		return domain.Plugin{}, pluginError("install plugin", err)
	}
	if err := p.record(ctx, caller, "plugin.install", rec); err != nil {
		return domain.Plugin{}, err
	}
	if in.Enable {
		enabled, err := p.Enable(ctx, rec.ID)
		if err != nil {
			_ = p.Uninstall(ctx, rec.ID)
			return domain.Plugin{}, err
		}
		return enabled, nil
	}
	return rec, nil
}

// Enable verifies the installed tree and turns the plugin on.
func (p *Plugins) Enable(ctx context.Context, id string) (domain.Plugin, error) {
	caller, _ := core.CallerFrom(ctx)
	pl, err := p.Get(ctx, id)
	if err != nil {
		return domain.Plugin{}, err
	}
	sum, err := p.d.Installer.Verify(pl.Dir)
	if err != nil {
		return domain.Plugin{}, pluginError("enable plugin", err)
	}
	if sum != pl.SHA256 {
		_ = p.d.Repo.SetEnabled(ctx, id, false, domain.HealthFailed)
		return domain.Plugin{}, pluginError("enable plugin", fmt.Errorf("%s changed on disk since install: %w", pl.Name, domain.ErrIntegrity))
	}
	for _, ref := range pl.Manifest.Contents.Themes {
		doc, err := readInTree(pl.Dir, ref.Path)
		if err != nil {
			return domain.Plugin{}, pluginError("enable plugin", err)
		}
		if _, err := p.d.Themes.ImportFromPlugin(ctx, id, doc); err != nil {
			return domain.Plugin{}, err
		}
	}
	if err := p.d.Repo.SetEnabled(ctx, id, true, domain.HealthOK); err != nil {
		return domain.Plugin{}, pluginError("enable plugin", err)
	}
	pl.Enabled, pl.Health = true, domain.HealthOK
	return pl, p.record(ctx, caller, "plugin.enable", pl)
}

// Disable turns the plugin off and takes its themes away.
func (p *Plugins) Disable(ctx context.Context, id string) (domain.Plugin, error) {
	caller, _ := core.CallerFrom(ctx)
	pl, err := p.Get(ctx, id)
	if err != nil {
		return domain.Plugin{}, err
	}
	if err := p.d.Themes.RemoveFromPlugin(ctx, id); err != nil {
		return domain.Plugin{}, err
	}
	if err := p.d.Repo.SetEnabled(ctx, id, false, pl.Health); err != nil {
		return domain.Plugin{}, pluginError("disable plugin", err)
	}
	pl.Enabled = false
	return pl, p.record(ctx, caller, "plugin.disable", pl)
}

// Uninstall removes the plugin, what it registered and its files.
func (p *Plugins) Uninstall(ctx context.Context, id string) error {
	caller, _ := core.CallerFrom(ctx)
	pl, err := p.Get(ctx, id)
	if err != nil {
		return err
	}
	if err := p.d.Themes.RemoveFromPlugin(ctx, id); err != nil {
		return err
	}
	if err := p.d.Repo.Delete(ctx, id); err != nil {
		return pluginError("uninstall plugin", err)
	}
	if err := p.d.Installer.Remove(pl.Name, pl.Version); err != nil {
		return pluginError("uninstall plugin", err)
	}
	return p.record(ctx, caller, "plugin.uninstall", pl)
}

// PanelToken is a token for a panel frame.
type PanelToken struct {
	ID        string
	Value     string
	Label     string
	Subject   string
	Scopes    []string
	CreatedAt time.Time
	ExpiresAt time.Time
}

// IssuePanelToken issues a token limited to the scopes the plugin's panels
// declare. The plugin must be enabled and have a panel.
func (p *Plugins) IssuePanelToken(ctx context.Context, id string, ttl time.Duration) (PanelToken, error) {
	pl, err := p.Get(ctx, id)
	if err != nil {
		return PanelToken{}, err
	}
	if !pl.Enabled || len(pl.Manifest.Contents.Panels) == 0 {
		return PanelToken{}, fmt.Errorf("issue panel token: %s is not an enabled plugin with a panel: %w", pl.Name, core.ErrConflict)
	}
	if ttl <= 0 {
		ttl = panelDefaultTTL
	}
	seen := map[string]bool{}
	var scopes []string
	for _, panel := range pl.Manifest.Contents.Panels {
		for _, s := range panel.Scopes {
			if !seen[s] {
				seen[s] = true
				scopes = append(scopes, s)
			}
		}
	}
	sort.Strings(scopes)
	tok, err := p.d.Tokens.Issue(ctx, core.Caller{Kind: core.CallerPlugin, Name: id, UID: -1}, scopes, ttl)
	if err != nil {
		return PanelToken{}, fmt.Errorf("issue panel token: %w", err)
	}
	return PanelToken{
		ID: tok.Value, Value: tok.Value, Label: "panel " + pl.Name, Subject: "plugin:" + id,
		Scopes: scopes, CreatedAt: p.d.Clock.Now().UTC(), ExpiresAt: tok.Expires,
	}, nil
}

// --- runners ---

// pluginRunner is a runner adapter backed by a manifest entry.
type pluginRunner struct {
	info core.RunnerInfo
	dir  string
	r    domain.Runner
}

func (r pluginRunner) Info() core.RunnerInfo { return r.info }

func (r pluginRunner) Exec(_ context.Context, _ core.Agent, socket string) (core.ExecSpec, error) {
	e := domain.RunnerExec(r.dir, r.r, socket)
	return core.ExecSpec{Path: e.Path, Args: e.Args, Env: e.Env, Dir: e.Dir}, nil
}

// RunnerEntries lists the built-in runners and the runners of enabled
// plugins, sorted by name. A plugin runner never replaces a built-in one.
func (p *Plugins) RunnerEntries(ctx context.Context) ([]domain.RunnerEntry, error) {
	entries := map[string]domain.RunnerEntry{}
	for _, b := range domain.BuiltinRunners(p.d.LibDir) {
		entries[b.Name] = domain.RunnerEntry{
			Name: b.Name, DisplayName: b.DisplayName, Version: b.Version, ProtocolVersion: b.ProtocolVersions[0],
			Builtin: true, Model: b.Model, Status: domain.RunnerReady, Description: b.Description,
		}
	}
	all, err := p.all(ctx)
	if err != nil {
		return nil, err
	}
	for _, pl := range all {
		if !pl.Enabled {
			continue
		}
		for _, r := range pl.Manifest.Contents.Runners {
			if _, taken := entries[r.Name]; taken {
				continue
			}
			entries[r.Name] = domain.RunnerEntry{
				Name: r.Name, DisplayName: r.Name, Version: pl.Version, ProtocolVersion: lastOr(r.ProtocolVersions, "1.0"),
				PluginID: pl.ID, Status: domain.RunnerReady, Description: r.Description,
			}
		}
	}
	out := make([]domain.RunnerEntry, 0, len(entries))
	for _, e := range entries {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// RunnerEntry returns one runner by name.
func (p *Plugins) RunnerEntry(ctx context.Context, name string) (domain.RunnerEntry, error) {
	entries, err := p.RunnerEntries(ctx)
	if err != nil {
		return domain.RunnerEntry{}, err
	}
	for _, e := range entries {
		if e.Name == name {
			return e, nil
		}
	}
	return domain.RunnerEntry{}, fmt.Errorf("runner %q: %w", name, core.ErrNotFound)
}

// Runners implements core.RunnerRegistry.
func (p *Plugins) Runners(ctx context.Context) ([]core.RunnerInfo, error) {
	entries, err := p.RunnerEntries(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]core.RunnerInfo, 0, len(entries))
	for _, e := range entries {
		out = append(out, core.RunnerInfo{Name: e.Name, Version: e.Version, Source: sourceOf(e)})
	}
	return out, nil
}

// Runner implements core.RunnerRegistry.
func (p *Plugins) Runner(ctx context.Context, name string) (core.RunnerAdapter, error) {
	for _, b := range domain.BuiltinRunners(p.d.LibDir) {
		if b.Name == name {
			return pluginRunner{info: core.RunnerInfo{Name: b.Name, Version: b.Version, Source: "builtin"}, dir: p.d.LibDir, r: b.Runner}, nil
		}
	}
	all, err := p.all(ctx)
	if err != nil {
		return nil, err
	}
	for _, pl := range all {
		if !pl.Enabled {
			continue
		}
		for _, r := range pl.Manifest.Contents.Runners {
			if r.Name == name {
				return pluginRunner{info: core.RunnerInfo{Name: name, Version: pl.Version, Source: pl.ID}, dir: pl.Dir, r: r}, nil
			}
		}
	}
	return nil, fmt.Errorf("runner %q: %w", name, core.ErrNotFound)
}

func sourceOf(e domain.RunnerEntry) string {
	if e.Builtin {
		return "builtin"
	}
	return e.PluginID
}

// --- helpers ---

func (p *Plugins) fetchAndNormalize(ctx context.Context, caller core.Caller, in InstallInput) (domain.Normalized, domain.Tree, error) {
	if in.Source == string(domain.SourceMCPRegistry) {
		n, err := p.normalizeRegistry(ctx, in)
		return n, domain.Tree{Cleanup: func() {}}, err
	}
	spec := specFor(in)
	if spec.Kind == domain.FetchLocal && caller.Kind != core.CallerHuman && caller.Kind != core.CallerSystem && caller.Kind != core.CallerHost {
		return domain.Normalized{}, domain.Tree{}, fmt.Errorf("%s may not install from a path on this machine: %w", caller.Kind, core.ErrForbidden)
	}
	tree, err := p.d.Fetcher.Fetch(ctx, spec)
	if err != nil {
		return domain.Normalized{}, domain.Tree{}, err
	}
	n, err := normalize(tree, in)
	if err != nil {
		tree.Cleanup()
		return domain.Normalized{}, domain.Tree{}, err
	}
	return n, tree, nil
}

func (p *Plugins) normalizeRegistry(ctx context.Context, in InstallInput) (domain.Normalized, error) {
	if p.d.Registry == nil {
		return domain.Normalized{}, fmt.Errorf("the MCP registry is not configured: %w", core.ErrUnavailable)
	}
	name, version, _ := strings.Cut(in.Ref, "@")
	if in.Version != "" {
		version = in.Version
	}
	srv, err := p.d.Registry.Server(ctx, name, version)
	if err != nil {
		return domain.Normalized{}, err
	}
	return domain.NormalizeRegistryServer(srv, domain.RegistryOptions{ContainerEngine: p.d.ContainerEngine})
}

func normalize(tree domain.Tree, in InstallInput) (domain.Normalized, error) {
	switch domain.SourceID(in.Source) {
	case domain.SourceAgentsOne:
		raw, err := fs.ReadFile(tree.FS, nativeManifest)
		if err != nil {
			return domain.Normalized{}, fmt.Errorf("%s not found in %s: %w", nativeManifest, in.Ref, domain.ErrNothingToInstall)
		}
		m, err := domain.ParseManifest(raw)
		if err != nil {
			return domain.Normalized{}, err
		}
		return domain.Normalized{Manifest: m}, nil
	case domain.SourceClaudeCode:
		return domain.NormalizeClaudePlugin(tree.FS, domain.ClaudeEntry{}, in.Ref, tree.SHA256)
	case domain.SourceAgentSkills:
		return domain.NormalizeAgentSkills(tree.FS, repoName(in.Ref), in.Ref)
	}
	return domain.Normalized{}, fmt.Errorf("source %q: %w", in.Source, domain.ErrUnsupportedSource)
}

var githubRef = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

// specFor decides how a ref is fetched.
func specFor(in InstallInput) domain.FetchSpec {
	ref := in.Ref
	switch {
	case strings.HasPrefix(ref, "npm:"):
		return domain.FetchSpec{Kind: domain.FetchNPM, Ref: strings.TrimPrefix(ref, "npm:"), Rev: in.Version}
	case strings.HasPrefix(ref, "https://") && archiveName(ref):
		return domain.FetchSpec{Kind: domain.FetchURL, Ref: ref}
	case strings.Contains(ref, "://") || strings.HasPrefix(ref, "git@"):
		return domain.FetchSpec{Kind: domain.FetchGit, Ref: ref, Rev: in.Version}
	case strings.HasPrefix(ref, "/") || strings.HasPrefix(ref, "./") || strings.HasPrefix(ref, "../"):
		return domain.FetchSpec{Kind: domain.FetchLocal, Ref: ref}
	case githubRef.MatchString(ref):
		return domain.FetchSpec{Kind: domain.FetchGitHub, Ref: ref, Rev: in.Version}
	}
	return domain.FetchSpec{Kind: domain.FetchLocal, Ref: ref}
}

func archiveName(ref string) bool {
	u := strings.ToLower(ref)
	return strings.HasSuffix(u, ".zip") || strings.HasSuffix(u, ".tar.gz") || strings.HasSuffix(u, ".tgz")
}

func repoName(ref string) string {
	base := path.Base(strings.TrimSuffix(strings.TrimSuffix(ref, "/"), ".git"))
	if base == "." || base == "/" || base == "" {
		return "skills"
	}
	return base
}

func validSource(s string) bool {
	switch domain.SourceID(s) {
	case domain.SourceClaudeCode, domain.SourceAgentsOne, domain.SourceAgentSkills, domain.SourceMCPRegistry:
		return true
	}
	return false
}

func (p *Plugins) findVersion(ctx context.Context, name, version string) (domain.Plugin, error) {
	all, err := p.d.Repo.ByName(ctx, name)
	if err != nil {
		return domain.Plugin{}, err
	}
	for _, pl := range all {
		if pl.Version == version {
			return pl, nil
		}
	}
	return domain.Plugin{}, domain.ErrNotFound
}

// all returns every installed plugin.
func (p *Plugins) all(ctx context.Context) ([]domain.Plugin, error) {
	var out []domain.Plugin
	after := ""
	for {
		batch, err := p.d.Repo.List(ctx, domain.PluginFilter{}, after, pluginMax)
		if err != nil {
			return nil, pluginError("list plugins", err)
		}
		out = append(out, batch...)
		if len(batch) < pluginMax {
			return out, nil
		}
		after = batch[len(batch)-1].ID
	}
}

func (p *Plugins) record(ctx context.Context, caller core.Caller, action string, pl domain.Plugin) error {
	actor := pluginSystemName
	if caller.Kind != "" && caller.Name != "" {
		actor = string(caller.Kind) + ":" + caller.Name
	}
	detail, _ := json.Marshal(map[string]string{"name": pl.Name, "version": pl.Version, "source": pl.Source})
	if err := p.d.Audit.Append(ctx, core.AuditEntry{Actor: actor, Action: action, Target: pl.ID, Detail: detail}); err != nil {
		return fmt.Errorf("%s: record audit: %w", action, err)
	}
	return nil
}

func readInTree(dir, rel string) ([]byte, error) {
	clean := strings.TrimPrefix(rel, "./")
	if !fs.ValidPath(clean) || clean == "." {
		return nil, fmt.Errorf("path %q: %w", rel, domain.ErrUnsafePath)
	}
	return fs.ReadFile(os.DirFS(dir), clean)
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func lastOr(list []string, def string) string {
	if len(list) == 0 {
		return def
	}
	return list[len(list)-1]
}

// page cuts a sorted slice after the cursor key.
func page[T any](items []T, after string, limit int, key func(T) string) ([]T, string, error) {
	switch {
	case limit <= 0:
		limit = pluginDefault
	case limit > pluginMax:
		limit = pluginMax
	}
	start := 0
	if after != "" {
		start = sort.Search(len(items), func(i int) bool { return key(items[i]) > after })
	}
	items = items[start:]
	next := ""
	if len(items) > limit {
		items = items[:limit]
		next = key(items[limit-1])
	}
	if items == nil {
		items = []T{}
	}
	return items, next, nil
}

// pluginError adds the core sentinel the API layer maps to a problem.
func pluginError(op string, err error) error {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return fmt.Errorf("%s: %w: %w", op, core.ErrNotFound, err)
	case errors.Is(err, domain.ErrAlreadyInstalled), errors.Is(err, domain.ErrInUse), errors.Is(err, domain.ErrBuiltin):
		return fmt.Errorf("%s: %w: %w", op, core.ErrConflict, err)
	case errors.Is(err, domain.ErrTooLarge):
		return fmt.Errorf("%s: %w: %w", op, core.NewProblem(413, "payload-too-large", err.Error()), err)
	case errors.Is(err, domain.ErrInvalidManifest), errors.Is(err, domain.ErrEmptyPlugin), errors.Is(err, domain.ErrUnsafePath),
		errors.Is(err, domain.ErrNotSemantic), errors.Is(err, domain.ErrUnsupportedSource), errors.Is(err, domain.ErrNothingToInstall),
		errors.Is(err, domain.ErrIntegrity), errors.Is(err, domain.ErrIncompatible):
		return fmt.Errorf("%s: %w: %w", op, core.ErrInvalid, err)
	}
	return fmt.Errorf("%s: %w", op, err)
}
