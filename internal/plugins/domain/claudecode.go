// SPDX-License-Identifier: MIT

package domain

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"
)

// ClaudeEntry is what a marketplace entry says about a plugin. It applies
// where the plugin has no manifest of its own.
type ClaudeEntry struct {
	Name        string
	Version     string
	Description string
	License     string
	Homepage    string
	Author      ClaudeAuthor
	// Raw is the whole entry as JSON. When the plugin has no
	// .claude-plugin/plugin.json it is read as one.
	Raw json.RawMessage
}

// ClaudeAuthor is the author of a Claude Code plugin.
type ClaudeAuthor struct {
	Name  string `json:"name"`
	Email string `json:"email"`
	URL   string `json:"url"`
}

// UnmarshalJSON accepts an author as a plain name or as an object.
func (a *ClaudeAuthor) UnmarshalJSON(b []byte) error {
	var name string
	if err := json.Unmarshal(b, &name); err == nil {
		*a = ClaudeAuthor{Name: name}
		return nil
	}
	type plain ClaudeAuthor
	return json.Unmarshal(b, (*plain)(a))
}

type claudeManifest struct {
	Name        string          `json:"name"`
	Version     string          `json:"version"`
	Description string          `json:"description"`
	Author      ClaudeAuthor    `json:"author"`
	Homepage    string          `json:"homepage"`
	License     string          `json:"license"`
	Skills      json.RawMessage `json:"skills"`
	Agents      json.RawMessage `json:"agents"`
	MCPServers  json.RawMessage `json:"mcpServers"`
}

// manifest fields Claude Code knows that Agents One has no place for.
var claudeUnmapped = map[string]string{
	"commands":     "Agents One has no slash commands",
	"hooks":        "Agents One has no Claude Code hook events",
	"lspServers":   "not supported",
	"outputStyles": "not supported",
	"workflows":    "not supported",
	"channels":     "not supported",
	"userConfig":   "not supported",
	"experimental": "Claude Code themes are terminal themes, not Agents One themes",
}

// NormalizeClaudePlugin reads a Claude Code plugin tree and writes the
// normalised manifest (plugins/SOURCES.md). ref is source.ref and sha256 the
// digest of the fetched archive, when there was one.
func NormalizeClaudePlugin(fsys fs.FS, entry ClaudeEntry, ref, sha256 string) (Normalized, error) {
	var out Normalized
	raw, fromEntry := claudeManifestBytes(fsys, entry)
	var cm claudeManifest
	if err := json.Unmarshal(raw, &cm); err != nil {
		return Normalized{}, errorSentinelFn("claude plugin manifest: %v", err)
	}
	var all map[string]json.RawMessage
	_ = json.Unmarshal(raw, &all)
	fillFromEntry(&cm, entry)
	if cm.Name == "" {
		return Normalized{}, errorSentinelFn("plugin has no name")
	}
	for _, key := range sortedKeys(claudeUnmapped) {
		if _, ok := all[key]; ok {
			out.Report.skip("%s: %s", key, claudeUnmapped[key])
		}
	}
	if !fromEntry && exists(fsys, "hooks/hooks.json") {
		out.Report.skip("hooks: %s", claudeUnmapped["hooks"])
	}
	if dirExists(fsys, "commands") {
		out.Report.skip("commands: %s", claudeUnmapped["commands"])
	}

	skills, err := claudeSkills(fsys, cm.Skills, &out.Report)
	if err != nil {
		return Normalized{}, err
	}
	templates, files, err := claudeAgents(fsys, cm.Agents, &out.Report)
	if err != nil {
		return Normalized{}, err
	}
	servers, err := claudeServers(fsys, cm.MCPServers, &out.Report)
	if err != nil {
		return Normalized{}, err
	}
	version, err := versionOrDefault(cm.Version)
	if err != nil {
		return Normalized{}, err
	}
	m := Manifest{
		Name:        cm.Name,
		Version:     version,
		Description: describe(cm.Description, fsys, cm.Name),
		Author:      claudeAuthor(cm.Author),
		License:     licenseOrNone(cm.License),
		Homepage:    cm.Homepage,
		Source:      Source{Kind: SourceClaudeCode, Ref: ref, SHA256: sha256},
		Compat:      Compat{AgentsOne: DefaultCompat},
		Contents:    Contents{Skills: skills, AgentTemplates: templates, McpServers: servers},
	}
	if len(m.Kinds()) == 0 {
		return Normalized{}, fmt.Errorf("%s has no skills, agents or MCP servers Agents One can use: %w", cm.Name, ErrNothingToInstall)
	}
	if err := m.Validate(); err != nil {
		return Normalized{}, err
	}
	out.Manifest, out.Files = m, files
	return out, nil
}

func claudeManifestBytes(fsys fs.FS, entry ClaudeEntry) (raw []byte, fromEntry bool) {
	if b, err := fs.ReadFile(fsys, ".claude-plugin/plugin.json"); err == nil {
		return b, false
	}
	if len(entry.Raw) > 0 {
		return entry.Raw, true
	}
	return []byte(`{}`), true
}

func fillFromEntry(cm *claudeManifest, e ClaudeEntry) {
	if cm.Name == "" {
		cm.Name = e.Name
	}
	if cm.Version == "" {
		cm.Version = e.Version
	}
	if cm.Description == "" {
		cm.Description = e.Description
	}
	if cm.License == "" {
		cm.License = e.License
	}
	if cm.Homepage == "" {
		cm.Homepage = e.Homepage
	}
	if cm.Author.Name == "" {
		cm.Author = e.Author
	}
}

func claudeAuthor(a ClaudeAuthor) Author {
	name := strings.TrimSpace(a.Name)
	if name == "" {
		name = "Unknown"
	}
	return Author{Name: cut(name, 128), Email: a.Email, URL: a.URL}
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func dirExists(fsys fs.FS, name string) bool {
	info, err := fs.Stat(fsys, name)
	return err == nil && info.IsDir()
}

// stringList reads "x" or ["x", "y"].
func stringList(raw json.RawMessage) ([]string, bool) {
	if len(raw) == 0 {
		return nil, false
	}
	var one string
	if err := json.Unmarshal(raw, &one); err == nil {
		return []string{one}, true
	}
	var many []string
	if err := json.Unmarshal(raw, &many); err == nil {
		return many, true
	}
	return nil, false
}

func claudeSkills(fsys fs.FS, raw json.RawMessage, rep *Report) ([]Skill, error) {
	paths, ok := stringList(raw)
	if !ok {
		paths = []string{"skills"}
	}
	var skills []Skill
	seen := map[string]bool{}
	add := func(dir string) error {
		doc, err := readSkill(fsys, dir)
		if err != nil {
			rep.skip("skill in %q: %v", dir, err)
			return nil
		}
		if path.Base(dir) != doc.Name {
			rep.skip("skill %q: name must equal its directory name %q", doc.Name, path.Base(dir))
			return nil
		}
		if !seen[doc.Name] {
			seen[doc.Name] = true
			skills = append(skills, Skill{Name: doc.Name, Path: "./" + dir, Description: doc.Description})
		}
		return nil
	}
	for _, p := range paths {
		norm, err := NormalizePath(p)
		if err != nil {
			return nil, err
		}
		dir := strings.TrimPrefix(norm, "./")
		if exists(fsys, path.Join(dir, "SKILL.md")) {
			_ = add(dir)
			continue
		}
		entries, err := fs.ReadDir(fsys, dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() && !strings.HasPrefix(e.Name(), ".") && exists(fsys, path.Join(dir, e.Name(), "SKILL.md")) {
				_ = add(path.Join(dir, e.Name()))
			}
		}
	}
	sort.Slice(skills, func(i, j int) bool { return skills[i].Name < skills[j].Name })
	return skills, nil
}

func readSkill(fsys fs.FS, dir string) (SkillDoc, error) {
	b, err := fs.ReadFile(fsys, path.Join(dir, "SKILL.md"))
	if err != nil {
		return SkillDoc{}, err
	}
	return ParseSkillMD(b)
}

// agentTemplate is agent-template.schema.json, as written for an agent file.
type agentTemplate struct {
	Name       string         `json:"name"`
	Kind       string         `json:"kind"`
	IsRoot     bool           `json:"isRoot"`
	Runner     runnerChoice   `json:"runner"`
	OneLine    string         `json:"oneLine"`
	Charter    string         `json:"charter"`
	Goals      []string       `json:"goals"`
	Skills     []string       `json:"skills"`
	MCPServers []string       `json:"mcpServers"`
	Limits     templateLimits `json:"limits"`
	Wake       templateWake   `json:"wake"`
	Schedules  []struct{}     `json:"schedules"`
}

type runnerChoice struct {
	Name   string         `json:"name"`
	Config map[string]any `json:"config"`
}

type templateLimits struct {
	CPUPercent int `json:"cpuPercent"`
	MemoryMiB  int `json:"memoryMiB"`
	Tasks      int `json:"tasks"`
}

type templateWake struct {
	Subscriptions []struct{} `json:"subscriptions"`
}

const maxCharterRunes = 65536

func claudeAgents(fsys fs.FS, raw json.RawMessage, rep *Report) ([]FileRef, []File, error) {
	paths, ok := stringList(raw)
	if !ok {
		paths = []string{"agents"}
	}
	var files []string
	for _, p := range paths {
		norm, err := NormalizePath(p)
		if err != nil {
			return nil, nil, err
		}
		rel := strings.TrimPrefix(norm, "./")
		if strings.HasSuffix(rel, ".md") && exists(fsys, rel) {
			files = append(files, rel)
			continue
		}
		entries, err := fs.ReadDir(fsys, rel)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
				files = append(files, path.Join(rel, e.Name()))
			}
		}
	}
	sort.Strings(files)
	var refs []FileRef
	var out []File
	seen := map[string]bool{}
	for _, f := range files {
		b, err := fs.ReadFile(fsys, f)
		if err != nil {
			return nil, nil, fmt.Errorf("read %s: %w", f, err)
		}
		tpl := agentFromMarkdown(path.Base(f), b)
		if seen[tpl.Name] {
			rep.skip("agent %s: another agent file has the same name", f)
			continue
		}
		seen[tpl.Name] = true
		data, err := json.MarshalIndent(tpl, "", "  ")
		if err != nil {
			return nil, nil, fmt.Errorf("encode agent template %s: %w", f, err)
		}
		rel := "agentsone/templates/" + tpl.Name + ".json"
		refs = append(refs, FileRef{Path: "./" + rel})
		out = append(out, File{Path: rel, Data: append(data, '\n')})
	}
	return refs, out, nil
}

func agentFromMarkdown(file string, b []byte) agentTemplate {
	stem := strings.TrimSuffix(file, ".md")
	name, description, body := stem, "", string(b)
	if front, rest, ok := splitFrontmatter(b); ok {
		var fm struct {
			Name        string `yaml:"name"`
			Description string `yaml:"description"`
		}
		if err := yaml.Unmarshal(front, &fm); err == nil {
			if fm.Name != "" {
				name = fm.Name
			}
			description, body = fm.Description, string(rest)
		}
	}
	name = SanitizeName(name)
	if len(name) > 31 {
		name = strings.Trim(name[:31], "-")
	}
	if description = oneLine(description); description == "" {
		description = "Agent " + name
	}
	return agentTemplate{
		Name: name, Kind: "chat", IsRoot: false,
		Runner:  runnerChoice{Name: "claude", Config: map[string]any{}},
		OneLine: cut(description, 255), Charter: cutRunes(strings.TrimSpace(body), maxCharterRunes),
		Goals: []string{}, Skills: []string{}, MCPServers: []string{},
		Limits:    templateLimits{CPUPercent: 100, MemoryMiB: 512, Tasks: 256},
		Wake:      templateWake{Subscriptions: []struct{}{}},
		Schedules: []struct{}{},
	}
}

func cutRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

type claudeMCP struct {
	Type    string            `json:"type"`
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
}

func claudeServers(fsys fs.FS, raw json.RawMessage, rep *Report) ([]McpServer, error) {
	var inline map[string]claudeMCP
	if len(raw) > 0 && json.Unmarshal(raw, &inline) == nil && inline != nil {
		return mapServers(inline, rep), nil
	}
	files, ok := stringList(raw)
	isDefault := false
	if !ok {
		if !exists(fsys, ".mcp.json") {
			return nil, nil
		}
		files, isDefault = []string{".mcp.json"}, true
	}
	var servers []McpServer
	for _, f := range files {
		if strings.HasSuffix(f, ".mcpb") {
			rep.skip("mcpServers %s: .mcpb bundles are not supported", f)
			continue
		}
		rel := f
		if !isDefault {
			norm, err := NormalizePath(f)
			if err != nil {
				return nil, err
			}
			rel = strings.TrimPrefix(norm, "./")
		}
		b, err := fs.ReadFile(fsys, rel)
		if err != nil {
			rep.skip("mcpServers %s: %v", f, err)
			continue
		}
		var file struct {
			Servers map[string]claudeMCP `json:"mcpServers"`
		}
		if err := json.Unmarshal(b, &file); err != nil {
			return nil, errorSentinelFn("%s: %v", f, err)
		}
		if file.Servers == nil {
			var plain map[string]claudeMCP
			if err := json.Unmarshal(b, &plain); err != nil {
				return nil, errorSentinelFn("%s: %v", f, err)
			}
			file.Servers = plain
		}
		servers = append(servers, mapServers(file.Servers, rep)...)
	}
	return servers, nil
}

func mapServers(in map[string]claudeMCP, rep *Report) []McpServer {
	var out []McpServer
	for _, name := range sortedKeys(in) {
		c := in[name]
		cn := SanitizeName(name)
		switch {
		case (c.Type == "" || c.Type == "stdio") && c.Command != "":
			out = append(out, McpServer{
				Name: cn, Transport: TransportStdio, Command: RewritePluginRoot(c.Command),
				Args: rewriteAll(c.Args), Env: rewriteMap(c.Env),
			})
		case (c.Type == "http" || c.Type == "streamable-http") && c.URL != "":
			out = append(out, McpServer{
				Name: cn, Transport: TransportHTTP, URL: RewritePluginRoot(c.URL), Headers: rewriteMap(c.Headers),
			})
		default:
			rep.skip("mcpServers %s: transport %q is not supported", name, c.Type)
		}
	}
	return out
}

func rewriteAll(in []string) []string {
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = RewritePluginRoot(s)
	}
	return out
}

func rewriteMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = RewritePluginRoot(v)
	}
	return out
}

// MarketplaceSourceType is how a marketplace entry says to fetch a plugin.
type MarketplaceSourceType string

// Marketplace source types.
const (
	MarketRelative  MarketplaceSourceType = "relative"
	MarketGitHub    MarketplaceSourceType = "github"
	MarketURL       MarketplaceSourceType = "url"
	MarketGitSubdir MarketplaceSourceType = "git-subdir"
	MarketNPM       MarketplaceSourceType = "npm"
	MarketArchive   MarketplaceSourceType = "archive"
	MarketCommand   MarketplaceSourceType = "command"
)

// MarketplaceSource is the where-from of one marketplace entry.
type MarketplaceSource struct {
	Type     MarketplaceSourceType `json:"-"`
	Path     string                `json:"path"`
	Repo     string                `json:"repo"`
	URL      string                `json:"url"`
	Ref      string                `json:"ref"`
	SHA      string                `json:"sha"`
	Package  string                `json:"package"`
	Version  string                `json:"version"`
	Registry string                `json:"registry"`
	SHA256   string                `json:"sha256"`
	Command  string                `json:"command"`
}

// UnmarshalJSON reads a relative path string or a typed object.
func (s *MarketplaceSource) UnmarshalJSON(b []byte) error {
	var rel string
	if err := json.Unmarshal(b, &rel); err == nil {
		*s = MarketplaceSource{Type: MarketRelative, Path: rel}
		return nil
	}
	type plain MarketplaceSource
	var obj struct {
		plain
		Source string `json:"source"`
	}
	if err := json.Unmarshal(b, &obj); err != nil {
		return err
	}
	*s = MarketplaceSource(obj.plain)
	s.Type = MarketplaceSourceType(obj.Source)
	switch s.Type {
	case MarketGitHub, MarketURL, MarketGitSubdir, MarketNPM, MarketArchive, MarketCommand:
		return nil
	}
	return fmt.Errorf("source type %q is not known", obj.Source)
}

// MarketplacePlugin is one entry of marketplace.json.
type MarketplacePlugin struct {
	Name        string
	Description string
	Version     string
	Source      MarketplaceSource
	// Entry is the entry as plugin manifest defaults.
	Entry ClaudeEntry
}

// Marketplace is .claude-plugin/marketplace.json.
type Marketplace struct {
	Name    string
	Owner   ClaudeAuthor
	Plugins []MarketplacePlugin
}

// ParseMarketplace reads and checks marketplace.json.
func ParseMarketplace(raw []byte) (Marketplace, error) {
	var doc struct {
		Name    string            `json:"name"`
		Owner   ClaudeAuthor      `json:"owner"`
		Plugins []json.RawMessage `json:"plugins"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return Marketplace{}, errorSentinelFn("marketplace.json: %v", err)
	}
	if doc.Name == "" || doc.Owner.Name == "" {
		return Marketplace{}, errorSentinelFn("marketplace.json needs name and owner.name")
	}
	mp := Marketplace{Name: doc.Name, Owner: doc.Owner}
	for i, p := range doc.Plugins {
		var e struct {
			ClaudeManifestFields
			Source *MarketplaceSource `json:"source"`
		}
		if err := json.Unmarshal(p, &e); err != nil {
			return Marketplace{}, errorSentinelFn("marketplace plugin %d: %v", i, err)
		}
		if e.Name == "" || e.Source == nil {
			return Marketplace{}, errorSentinelFn("marketplace plugin %d needs name and source", i)
		}
		mp.Plugins = append(mp.Plugins, MarketplacePlugin{
			Name: e.Name, Description: e.Description, Version: e.Version, Source: *e.Source,
			Entry: ClaudeEntry{
				Name: e.Name, Version: e.Version, Description: e.Description, License: e.License,
				Homepage: e.Homepage, Author: e.Author, Raw: p,
			},
		})
	}
	return mp, nil
}

// ClaudeManifestFields are the scalar fields marketplace entries share with plugin.json.
type ClaudeManifestFields struct {
	Name        string       `json:"name"`
	Version     string       `json:"version"`
	Description string       `json:"description"`
	Author      ClaudeAuthor `json:"author"`
	Homepage    string       `json:"homepage"`
	License     string       `json:"license"`
}
