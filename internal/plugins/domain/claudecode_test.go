// SPDX-License-Identifier: MIT

package domain_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/SDK-E/agentsone/internal/plugins/domain"
)

const claudePluginJSON = `{
  "name": "garden-kit",
  "version": "2.1.0",
  "description": "Garden tools.",
  "author": {"name": "Example Gardens", "email": "dev@example.com", "url": "https://example.com"},
  "homepage": "https://example.com/garden",
  "license": "MIT",
  "mcpServers": {
    "garden-db": {"command": "${CLAUDE_PLUGIN_ROOT}/bin/garden", "args": ["--db", "${CLAUDE_PLUGIN_ROOT}/data/g.db"], "env": {"LOG": "info", "ROOT": "${CLAUDE_PLUGIN_ROOT}"}},
    "weather": {"type": "http", "url": "https://weather.example.com/mcp", "headers": {"X-Client": "a"}},
    "legacy": {"type": "sse", "url": "https://old.example.com/sse"}
  },
  "hooks": {"PostToolUse": []},
  "commands": "./commands"
}`

func claudeTree() fstest.MapFS {
	return fstest.MapFS{
		".claude-plugin/plugin.json": {Data: []byte(claudePluginJSON)},
		"skills/prune/SKILL.md":      {Data: []byte("---\nname: prune\ndescription: Prune roses.\n---\nSteps")},
		"agents/gardener.md":         {Data: []byte("---\nname: gardener\ndescription: Tends the garden.\n---\nYou tend the garden.\n")},
		"commands/water.md":          {Data: []byte("water")},
		"README.md":                  {Data: []byte("# Garden kit\n")},
	}
}

func TestNormalizeClaudePlugin_MapsEveryKindItCan(t *testing.T) {
	t.Parallel()

	got, err := domain.NormalizeClaudePlugin(claudeTree(), domain.ClaudeEntry{}, "example-marketplace/garden-kit", "")
	if err != nil {
		t.Fatalf("NormalizeClaudePlugin() error = %v", err)
	}

	m := got.Manifest
	if m.Name != "garden-kit" || m.Version != "2.1.0" || m.License != "MIT" || m.Homepage != "https://example.com/garden" {
		t.Errorf("header = %+v", m)
	}
	if m.Author.Name != "Example Gardens" || m.Author.Email != "dev@example.com" {
		t.Errorf("author = %+v", m.Author)
	}
	if m.Source.Kind != domain.SourceClaudeCode || m.Source.Ref != "example-marketplace/garden-kit" {
		t.Errorf("source = %+v", m.Source)
	}
	if len(m.Contents.Skills) != 1 || m.Contents.Skills[0].Path != "./skills/prune" {
		t.Errorf("skills = %+v", m.Contents.Skills)
	}
	if err := m.Validate(); err != nil {
		t.Errorf("manifest invalid: %v", err)
	}
}

func TestNormalizeClaudePlugin_RewritesPluginRootAndMapsMCPTransports(t *testing.T) {
	t.Parallel()
	got, err := domain.NormalizeClaudePlugin(claudeTree(), domain.ClaudeEntry{}, "m/garden-kit", "")
	if err != nil {
		t.Fatal(err)
	}

	servers := map[string]domain.McpServer{}
	for _, s := range got.Manifest.Contents.McpServers {
		servers[s.Name] = s
	}
	db := servers["garden-db"]
	if db.Transport != "stdio" || db.Command != "${PLUGIN_ROOT}/bin/garden" || db.Args[1] != "${PLUGIN_ROOT}/data/g.db" || db.Env["ROOT"] != "${PLUGIN_ROOT}" {
		t.Errorf("garden-db = %+v", db)
	}
	if w := servers["weather"]; w.Transport != "streamable-http" || w.URL != "https://weather.example.com/mcp" || w.Headers["X-Client"] != "a" {
		t.Errorf("weather = %+v", w)
	}
	if _, ok := servers["legacy"]; ok {
		t.Error("the sse server was mapped")
	}
	if !reportMentions(got.Report.NotMapped, "legacy") {
		t.Errorf("report does not name the sse server: %v", got.Report.NotMapped)
	}
}

func TestNormalizeClaudePlugin_ListsWhatItCannotMap(t *testing.T) {
	t.Parallel()
	got, err := domain.NormalizeClaudePlugin(claudeTree(), domain.ClaudeEntry{}, "m/garden-kit", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"hooks", "commands"} {
		if !reportMentions(got.Report.NotMapped, want) {
			t.Errorf("report %v does not mention %s", got.Report.NotMapped, want)
		}
	}
}

func TestNormalizeClaudePlugin_AgentMarkdownBecomesAnAgentTemplate(t *testing.T) {
	t.Parallel()
	got, err := domain.NormalizeClaudePlugin(claudeTree(), domain.ClaudeEntry{}, "m/garden-kit", "")
	if err != nil {
		t.Fatal(err)
	}

	if len(got.Manifest.Contents.AgentTemplates) != 1 {
		t.Fatalf("agentTemplates = %+v", got.Manifest.Contents.AgentTemplates)
	}
	ref := got.Manifest.Contents.AgentTemplates[0].Path
	var file *domain.File
	for i := range got.Files {
		if "./"+got.Files[i].Path == ref {
			file = &got.Files[i]
		}
	}
	if file == nil {
		t.Fatalf("no generated file for %s in %+v", ref, got.Files)
	}
	var tpl map[string]any
	if err := json.Unmarshal(file.Data, &tpl); err != nil {
		t.Fatal(err)
	}
	runner, _ := tpl["runner"].(map[string]any)
	if tpl["name"] != "gardener" || tpl["kind"] != "chat" || tpl["isRoot"] != false || runner["name"] != "claude" ||
		tpl["oneLine"] != "Tends the garden." || !strings.HasPrefix(tpl["charter"].(string), "You tend the garden.") {
		t.Errorf("template = %v", tpl)
	}
}

func TestNormalizeClaudePlugin_WithoutManifestUsesStandardLayoutAndEntry(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"skills/prune/SKILL.md": {Data: []byte("---\nname: prune\ndescription: Prune.\n---\n")},
		".mcp.json":             {Data: []byte(`{"mcpServers": {"one": {"command": "npx", "args": ["-y", "pkg"]}}}`)},
		"README.md":             {Data: []byte("# Title\n\nFirst real line of the readme.\n")},
	}
	entry := domain.ClaudeEntry{Name: "from-entry", Version: "1.0.0", License: "Apache-2.0"}

	got, err := domain.NormalizeClaudePlugin(fsys, entry, "m/from-entry", "")
	if err != nil {
		t.Fatalf("NormalizeClaudePlugin() error = %v", err)
	}

	m := got.Manifest
	if m.Name != "from-entry" || m.Version != "1.0.0" || m.License != "Apache-2.0" {
		t.Errorf("header = %+v", m)
	}
	if m.Description != "Title" {
		t.Errorf("description = %q, want the first README line", m.Description)
	}
	if len(m.Contents.McpServers) != 1 || m.Contents.McpServers[0].Name != "one" || m.Contents.McpServers[0].Command != "npx" {
		t.Errorf("servers = %+v", m.Contents.McpServers)
	}
}

func TestNormalizeClaudePlugin_Defaults(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		".claude-plugin/plugin.json": {Data: []byte(`{"name": "bare"}`)},
		"skills/a/SKILL.md":          {Data: []byte("---\nname: a\ndescription: a\n---\n")},
	}

	got, err := domain.NormalizeClaudePlugin(fsys, domain.ClaudeEntry{}, "m/bare", "")
	if err != nil {
		t.Fatal(err)
	}

	m := got.Manifest
	if m.Version != "0.0.0" || m.License != "NOASSERTION" || m.Description != "bare" || m.Compat.AgentsOne != ">=0.1.0" {
		t.Errorf("defaults = version %q license %q description %q compat %q", m.Version, m.License, m.Description, m.Compat.AgentsOne)
	}
}

func TestNormalizeClaudePlugin_Refusals(t *testing.T) {
	t.Parallel()
	skill := fstest.MapFile{Data: []byte("---\nname: a\ndescription: a\n---\n")}
	tests := []struct {
		name string
		fsys fstest.MapFS
		want error
	}{
		{"version not semantic", fstest.MapFS{".claude-plugin/plugin.json": {Data: []byte(`{"name":"x","version":"latest"}`)}, "skills/a/SKILL.md": &skill}, domain.ErrNotSemantic},
		{"skills path leaves the root", fstest.MapFS{".claude-plugin/plugin.json": {Data: []byte(`{"name":"x","skills":"../outside"}`)}}, domain.ErrUnsafePath},
		{"mcp path leaves the root", fstest.MapFS{".claude-plugin/plugin.json": {Data: []byte(`{"name":"x","mcpServers":"../../etc/x.json"}`)}}, domain.ErrUnsafePath},
		{"nothing mappable", fstest.MapFS{".claude-plugin/plugin.json": {Data: []byte(`{"name":"x","commands":"./c"}`)}, "commands/a.md": {Data: []byte("a")}}, domain.ErrNothingToInstall},
		{"manifest is not JSON", fstest.MapFS{".claude-plugin/plugin.json": {Data: []byte(`{`)}}, domain.ErrInvalidManifest},
		{"no name anywhere", fstest.MapFS{"skills/a/SKILL.md": &skill}, domain.ErrInvalidManifest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := domain.NormalizeClaudePlugin(tt.fsys, domain.ClaudeEntry{}, "m/x", "")
			if !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestParseMarketplace_ReadsEverySourceType(t *testing.T) {
	t.Parallel()
	raw := `{
  "name": "example-marketplace",
  "owner": {"name": "Example Co"},
  "plugins": [
    {"name": "rel", "source": "./plugins/rel", "description": "Relative."},
    {"name": "gh", "source": {"source": "github", "repo": "owner/repo", "ref": "v1", "sha": "abc"}},
    {"name": "url", "source": {"source": "url", "url": "https://git.example.com/x.git"}},
    {"name": "sub", "source": {"source": "git-subdir", "url": "https://git.example.com/x.git", "path": "plugins/sub"}},
    {"name": "npm", "source": {"source": "npm", "package": "@scope/pkg", "version": "1.2.3"}},
    {"name": "arc", "source": {"source": "archive", "url": "https://example.com/a.zip", "sha256": "` + strings.Repeat("a", 64) + `"}},
    {"name": "cmd", "source": {"source": "command", "command": "make"}}
  ]
}`

	mp, err := domain.ParseMarketplace([]byte(raw))
	if err != nil {
		t.Fatalf("ParseMarketplace() error = %v", err)
	}

	if mp.Name != "example-marketplace" || mp.Owner.Name != "Example Co" || len(mp.Plugins) != 7 {
		t.Fatalf("marketplace = %+v", mp)
	}
	kinds := make([]string, 0, 7)
	for _, p := range mp.Plugins {
		kinds = append(kinds, string(p.Source.Type))
	}
	want := "relative,github,url,git-subdir,npm,archive,command"
	if strings.Join(kinds, ",") != want {
		t.Errorf("source types = %v, want %s", kinds, want)
	}
	if mp.Plugins[1].Source.Repo != "owner/repo" || mp.Plugins[3].Source.Path != "plugins/sub" || mp.Plugins[4].Source.Package != "@scope/pkg" {
		t.Errorf("source fields wrong: %+v", mp.Plugins)
	}
}

func TestParseMarketplace_Refusals(t *testing.T) {
	t.Parallel()
	tests := map[string]string{
		"no name":               `{"owner":{"name":"x"},"plugins":[]}`,
		"no owner name":         `{"name":"m","owner":{},"plugins":[]}`,
		"plugin without source": `{"name":"m","owner":{"name":"x"},"plugins":[{"name":"a"}]}`,
		"unknown source type":   `{"name":"m","owner":{"name":"x"},"plugins":[{"name":"a","source":{"source":"ftp"}}]}`,
		"not JSON":              `{`,
	}
	for name, raw := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := domain.ParseMarketplace([]byte(raw)); !errors.Is(err, domain.ErrInvalidManifest) {
				t.Fatalf("error = %v, want ErrInvalidManifest", err)
			}
		})
	}
}

func reportMentions(items []string, word string) bool {
	for _, s := range items {
		if strings.Contains(s, word) {
			return true
		}
	}
	return false
}
