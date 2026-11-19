// SPDX-License-Identifier: MIT

package domain_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"testing/fstest"
	"unicode/utf8"

	"github.com/SDK-E/agentsone/internal/plugins/domain"
)

// moreSkill is a valid SKILL.md for name.
func moreSkill(name string) string {
	return "---\nname: " + name + "\ndescription: Skill " + name + ".\n---\nBody"
}

// morePlugin is a plugin.json with the given extra JSON members.
func morePlugin(extra string) fstest.MapFS {
	body := `{"name":"more-kit"`
	if extra != "" {
		body += "," + extra
	}
	return fstest.MapFS{".claude-plugin/plugin.json": {Data: []byte(body + "}")}}
}

func withFiles(fsys fstest.MapFS, files map[string]string) fstest.MapFS {
	for name, data := range files {
		fsys[name] = &fstest.MapFile{Data: []byte(data)}
	}
	return fsys
}

func TestClaudeAuthor_UnmarshalJSON(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		raw     string
		want    domain.ClaudeAuthor
		wantErr bool
	}{
		{"plain name", `"Ada"`, domain.ClaudeAuthor{Name: "Ada"}, false},
		{"object", `{"name":"Ada","email":"a@example.com","url":"https://a.example"}`,
			domain.ClaudeAuthor{Name: "Ada", Email: "a@example.com", URL: "https://a.example"}, false},
		{"number", `5`, domain.ClaudeAuthor{}, true},
		{"list", `["Ada"]`, domain.ClaudeAuthor{}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var got domain.ClaudeAuthor
			err := json.Unmarshal([]byte(tt.raw), &got)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Unmarshal() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err == nil && got != tt.want {
				t.Errorf("author = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestNormalizeClaudePlugin_AuthorForms(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		author     string
		wantAuthor string
		wantErr    bool
	}{
		{"string author", `"author":"Ada Lovelace"`, "Ada Lovelace", false},
		{"object author", `"author":{"name":"Grace"}`, "Grace", false},
		{"missing author", ``, "Unknown", false},
		{"blank author", `"author":{"name":"   "}`, "Unknown", false},
		{"author of the wrong type", `"author":5`, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			extra := `"skills":"skills"`
			if tt.author != "" {
				extra += "," + tt.author
			}
			fsys := withFiles(morePlugin(extra), map[string]string{"skills/a/SKILL.md": moreSkill("a")})
			got, err := domain.NormalizeClaudePlugin(fsys, domain.ClaudeEntry{}, "ref", "")
			if tt.wantErr {
				if !errors.Is(err, domain.ErrInvalidManifest) {
					t.Fatalf("error = %v, want ErrInvalidManifest", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("NormalizeClaudePlugin() error = %v", err)
			}
			if got.Manifest.Author.Name != tt.wantAuthor {
				t.Errorf("author = %q, want %q", got.Manifest.Author.Name, tt.wantAuthor)
			}
		})
	}
}

func TestNormalizeClaudePlugin_SkillsFieldForms(t *testing.T) {
	t.Parallel()
	tree := map[string]string{
		"skills/a/SKILL.md":       moreSkill("a"),
		"extra/b/SKILL.md":        moreSkill("b"),
		"extra/.hidden/SKILL.md":  moreSkill("hidden"),
		"single/solo/SKILL.md":    moreSkill("solo"),
		"wrongdir/other/SKILL.md": moreSkill("not-other"),
		"broken/bad/SKILL.md":     "no frontmatter here",
	}
	tests := []struct {
		name         string
		field        string
		wantSkills   []string
		wantNotMaps  string
		wantNothing  bool
		wantUnsafe   bool
		wantNotMapOn bool
	}{
		{name: "absent field uses skills folder", field: ``, wantSkills: []string{"a"}},
		{name: "string folder", field: `"skills":"./extra"`, wantSkills: []string{"b"}},
		{name: "string pointing at one skill", field: `"skills":"single/solo"`, wantSkills: []string{"solo"}},
		{name: "array of folders", field: `"skills":["skills","extra"]`, wantSkills: []string{"a", "b"}},
		{name: "array repeating a skill keeps one", field: `"skills":["skills","./skills/a"]`, wantSkills: []string{"a"}},
		{name: "object form is not a path list and falls back to the folder", field: `"skills":{"a":1}`, wantSkills: []string{"a"}},
		{name: "number form falls back to the folder", field: `"skills":7`, wantSkills: []string{"a"}},
		{name: "missing folder is skipped", field: `"skills":["nowhere","skills"]`, wantSkills: []string{"a"}},
		{name: "invalid skill is reported", field: `"skills":["skills","broken"]`, wantSkills: []string{"a"}, wantNotMaps: "broken"},
		{name: "name differing from directory is reported", field: `"skills":["skills","wrongdir"]`, wantSkills: []string{"a"}, wantNotMaps: "not-other"},
		{name: "traversal is refused", field: `"skills":"../outside"`, wantUnsafe: true},
		{name: "absolute path is refused", field: `"skills":["/etc"]`, wantUnsafe: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			extra := tt.field
			fsys := withFiles(morePlugin(extra), tree)
			got, err := domain.NormalizeClaudePlugin(fsys, domain.ClaudeEntry{}, "ref", "")
			if tt.wantUnsafe {
				if !errors.Is(err, domain.ErrUnsafePath) {
					t.Fatalf("error = %v, want ErrUnsafePath", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("NormalizeClaudePlugin() error = %v", err)
			}
			var names []string
			for _, s := range got.Manifest.Contents.Skills {
				names = append(names, s.Name)
			}
			if !equalStrings(names, tt.wantSkills) {
				t.Errorf("skills = %v, want %v", names, tt.wantSkills)
			}
			if tt.wantNotMaps != "" && !reportMentions(got.Report.NotMapped, tt.wantNotMaps) {
				t.Errorf("NotMapped = %v, want a mention of %q", got.Report.NotMapped, tt.wantNotMaps)
			}
		})
	}
}

func TestNormalizeClaudePlugin_AgentsFieldForms(t *testing.T) {
	t.Parallel()
	tree := map[string]string{
		"agents/alpha.md":         "---\nname: alpha\ndescription: First agent.\n---\nYou are alpha.",
		"agents/beta.md":          "---\nname: Beta Agent!\ndescription: |\n  Second\n  agent.\n---\nYou are beta.",
		"agents/notes.txt":        "not an agent",
		"extra/gamma.md":          "No frontmatter, so the file name is the agent name.",
		"extra/delta.md":          "---\nname: alpha\ndescription: Same name as alpha.\n---\nDuplicate.",
		"extra/broken.md":         "---\n: [unclosed\n---\nBody of a broken file.",
		"extra/" + longName(40):   "---\ndescription: Long name.\n---\nLong.",
		"single/only.md":          "---\ndescription:\n---\nNo description.",
		"agents/with-charter.md":  "---\nname: charter\ndescription: Charter.\n---\n" + strings.Repeat("é", 70000),
		"agents/blank-desc/x.txt": "ignored",
	}
	tests := []struct {
		name        string
		field       string
		wantNames   []string
		wantNotMaps string
		wantUnsafe  bool
	}{
		{name: "absent field reads the agents folder", field: ``, wantNames: []string{"alpha", "beta-agent", "charter"}},
		{name: "string folder", field: `"agents":"./extra"`, wantNames: []string{"alpha", "broken", longName(31)[:31], "gamma"}},
		{name: "string file", field: `"agents":"single/only.md"`, wantNames: []string{"only"}},
		{name: "array of file and folder", field: `"agents":["agents/alpha.md","extra"]`, wantNames: []string{"alpha", "broken", longName(31)[:31], "gamma"}, wantNotMaps: "same name"},
		{name: "object form falls back to the folder", field: `"agents":{"x":1}`, wantNames: []string{"alpha", "beta-agent", "charter"}},
		{name: "missing folder gives no agents", field: `"agents":"nowhere"`, wantNames: nil},
		{name: "traversal is refused", field: `"agents":"../x"`, wantUnsafe: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			extra := `"skills":"skills"`
			if tt.field != "" {
				extra += "," + tt.field
			}
			files := map[string]string{"skills/s/SKILL.md": moreSkill("s")}
			for k, v := range tree {
				files[k] = v
			}
			fsys := withFiles(morePlugin(extra), files)
			got, err := domain.NormalizeClaudePlugin(fsys, domain.ClaudeEntry{}, "ref", "")
			if tt.wantUnsafe {
				if !errors.Is(err, domain.ErrUnsafePath) {
					t.Fatalf("error = %v, want ErrUnsafePath", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("NormalizeClaudePlugin() error = %v", err)
			}
			var names []string
			for _, f := range got.Files {
				var tpl struct {
					Name    string `json:"name"`
					OneLine string `json:"oneLine"`
					Charter string `json:"charter"`
				}
				if err := json.Unmarshal(f.Data, &tpl); err != nil {
					t.Fatalf("template %s: %v", f.Path, err)
				}
				names = append(names, tpl.Name)
				if n := utf8.RuneCountInString(tpl.Charter); n > 65536 {
					t.Errorf("template %s charter has %d runes, want at most 65536", tpl.Name, n)
				}
				if tpl.OneLine == "" {
					t.Errorf("template %s has no one-line description", tpl.Name)
				}
			}
			if len(got.Manifest.Contents.AgentTemplates) != len(tt.wantNames) {
				t.Errorf("agent templates = %d, want %d", len(got.Manifest.Contents.AgentTemplates), len(tt.wantNames))
			}
			if !sameSet(names, tt.wantNames) {
				t.Errorf("agent names = %v, want %v", names, tt.wantNames)
			}
			if tt.wantNotMaps != "" && !reportMentions(got.Report.NotMapped, tt.wantNotMaps) {
				t.Errorf("NotMapped = %v, want a mention of %q", got.Report.NotMapped, tt.wantNotMaps)
			}
		})
	}
}

func longName(n int) string { return strings.Repeat("a", n) + ".md" }

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := map[string]int{}
	for _, s := range a {
		seen[s]++
	}
	for _, s := range b {
		seen[s]--
	}
	for _, n := range seen {
		if n != 0 {
			return false
		}
	}
	return true
}

func TestNormalizeClaudePlugin_AgentCharterIsCutAtTheLimit(t *testing.T) {
	t.Parallel()
	fsys := withFiles(morePlugin(`"agents":"agents"`), map[string]string{
		"agents/big.md":   "---\nname: big\ndescription: Big.\n---\n" + strings.Repeat("é", 70000),
		"agents/small.md": "---\nname: small\ndescription: Small.\n---\nshort charter",
	})
	got, err := domain.NormalizeClaudePlugin(fsys, domain.ClaudeEntry{}, "ref", "")
	if err != nil {
		t.Fatalf("NormalizeClaudePlugin() error = %v", err)
	}
	lengths := map[string]int{}
	for _, f := range got.Files {
		var tpl struct {
			Name    string `json:"name"`
			Charter string `json:"charter"`
		}
		if err := json.Unmarshal(f.Data, &tpl); err != nil {
			t.Fatal(err)
		}
		lengths[tpl.Name] = utf8.RuneCountInString(tpl.Charter)
	}
	if lengths["big"] != 65536 {
		t.Errorf("big charter = %d runes, want 65536", lengths["big"])
	}
	if lengths["small"] != len("short charter") {
		t.Errorf("small charter = %d runes, want %d", lengths["small"], len("short charter"))
	}
}

func TestNormalizeClaudePlugin_McpServersFieldForms(t *testing.T) {
	t.Parallel()
	stdio := `{"command":"${CLAUDE_PLUGIN_ROOT}/bin/x","args":["a"]}`
	remote := `{"type":"streamable-http","url":"https://m.example/mcp","headers":{"X-A":"1"}}`
	tests := []struct {
		name        string
		field       string
		files       map[string]string
		wantServers []string
		wantNotMaps string
		wantErr     error
	}{
		{name: "inline map", field: `"mcpServers":{"Alpha Server":` + stdio + `,"web":` + remote + `}`, wantServers: []string{"alpha-server", "web"}},
		{name: "inline map with an unsupported transport", field: `"mcpServers":{"ok":` + stdio + `,"old":{"type":"sse","url":"https://o.example"}}`, wantServers: []string{"ok"}, wantNotMaps: "old"},
		{name: "path string with mcpServers wrapper", field: `"mcpServers":"./servers.json"`, files: map[string]string{"servers.json": `{"mcpServers":{"wrapped":` + stdio + `}}`}, wantServers: []string{"wrapped"}},
		{name: "path string with a plain map", field: `"mcpServers":"servers.json"`, files: map[string]string{"servers.json": `{"plain":` + stdio + `}`}, wantServers: []string{"plain"}},
		{name: "array of paths", field: `"mcpServers":["one.json","two.json"]`, files: map[string]string{
			"one.json": `{"mcpServers":{"one":` + stdio + `}}`, "two.json": `{"two":` + remote + `}`}, wantServers: []string{"one", "two"}},
		{name: "mcpb bundle is reported", field: `"mcpServers":["bundle.mcpb","one.json"]`, files: map[string]string{
			"one.json": `{"mcpServers":{"one":` + stdio + `}}`}, wantServers: []string{"one"}, wantNotMaps: "mcpb"},
		{name: "missing file is reported and not fatal", field: `"mcpServers":["gone.json","one.json"]`, files: map[string]string{
			"one.json": `{"mcpServers":{"one":` + stdio + `}}`}, wantServers: []string{"one"}, wantNotMaps: "gone.json"},
		{name: "default .mcp.json with wrapper", field: ``, files: map[string]string{".mcp.json": `{"mcpServers":{"dflt":` + stdio + `}}`}, wantServers: []string{"dflt"}},
		{name: "default .mcp.json with a plain map", field: ``, files: map[string]string{".mcp.json": `{"dflt":` + stdio + `}`}, wantServers: []string{"dflt"}},
		{name: "field of the wrong type falls back to .mcp.json", field: `"mcpServers":7`, files: map[string]string{".mcp.json": `{"dflt":` + stdio + `}`}, wantServers: []string{"dflt"}},
		{name: "bad JSON file is refused", field: `"mcpServers":"servers.json"`, files: map[string]string{"servers.json": `{`}, wantErr: domain.ErrInvalidManifest},
		{name: "file whose entries are not objects is refused", field: `"mcpServers":"servers.json"`, files: map[string]string{"servers.json": `{"a":5}`}, wantErr: domain.ErrInvalidManifest},
		{name: "traversal is refused", field: `"mcpServers":"../servers.json"`, wantErr: domain.ErrUnsafePath},
		{name: "http entry without url is reported", field: `"mcpServers":{"ok":` + stdio + `,"nourl":{"type":"http"}}`, wantServers: []string{"ok"}, wantNotMaps: "nourl"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			extra := `"skills":"skills"`
			if tt.field != "" {
				extra += "," + tt.field
			}
			files := map[string]string{"skills/s/SKILL.md": moreSkill("s")}
			for k, v := range tt.files {
				files[k] = v
			}
			got, err := domain.NormalizeClaudePlugin(withFiles(morePlugin(extra), files), domain.ClaudeEntry{}, "ref", "")
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("error = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("NormalizeClaudePlugin() error = %v", err)
			}
			var names []string
			for _, s := range got.Manifest.Contents.McpServers {
				names = append(names, s.Name)
			}
			if !sameSet(names, tt.wantServers) {
				t.Errorf("servers = %v, want %v", names, tt.wantServers)
			}
			if tt.wantNotMaps != "" && !reportMentions(got.Report.NotMapped, tt.wantNotMaps) {
				t.Errorf("NotMapped = %v, want a mention of %q", got.Report.NotMapped, tt.wantNotMaps)
			}
		})
	}
}

func TestNormalizeClaudePlugin_McpServerValuesAreRewritten(t *testing.T) {
	t.Parallel()
	fsys := withFiles(morePlugin(`"mcpServers":{"s":{"command":"${CLAUDE_PLUGIN_ROOT}/b","args":["${CLAUDE_PLUGIN_ROOT}/a"],"env":{"R":"${CLAUDE_PLUGIN_ROOT}"}}}`), nil)
	got, err := domain.NormalizeClaudePlugin(fsys, domain.ClaudeEntry{}, "ref", "")
	if err != nil {
		t.Fatalf("NormalizeClaudePlugin() error = %v", err)
	}
	s := got.Manifest.Contents.McpServers[0]
	if s.Command != "${PLUGIN_ROOT}/b" || s.Args[0] != "${PLUGIN_ROOT}/a" || s.Env["R"] != "${PLUGIN_ROOT}" {
		t.Errorf("server = %+v, want ${CLAUDE_PLUGIN_ROOT} rewritten to ${PLUGIN_ROOT}", s)
	}
}

func TestNormalizeClaudePlugin_ManifestAndEntryRefusals(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		fsys    fstest.MapFS
		entry   domain.ClaudeEntry
		wantErr error
	}{
		{"manifest is not JSON", fstest.MapFS{".claude-plugin/plugin.json": {Data: []byte(`{`)}}, domain.ClaudeEntry{}, domain.ErrInvalidManifest},
		{"no manifest, no entry, no name", fstest.MapFS{}, domain.ClaudeEntry{}, domain.ErrInvalidManifest},
		{"version is not semantic", withFiles(morePlugin(`"version":"v1","skills":"skills"`), map[string]string{"skills/a/SKILL.md": moreSkill("a")}), domain.ClaudeEntry{}, domain.ErrNotSemantic},
		{"nothing to install", morePlugin(`"description":"Empty."`), domain.ClaudeEntry{}, domain.ErrNothingToInstall},
		{"entry with a name only has nothing to install", fstest.MapFS{}, domain.ClaudeEntry{Name: "named"}, domain.ErrNothingToInstall},
		{"entry raw is not JSON", fstest.MapFS{}, domain.ClaudeEntry{Name: "named", Raw: json.RawMessage(`[`)}, domain.ErrInvalidManifest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if _, err := domain.NormalizeClaudePlugin(tt.fsys, tt.entry, "ref", ""); !errors.Is(err, tt.wantErr) {
				t.Fatalf("error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestNormalizeClaudePlugin_FillsGapsFromTheEntryAndFallsBackOnDescription(t *testing.T) {
	t.Parallel()
	entry := domain.ClaudeEntry{
		Name: "from-entry", Version: "3.0.0", Description: "Entry description.", License: "Apache-2.0",
		Homepage: "https://entry.example", Author: domain.ClaudeAuthor{Name: "Entry Author"},
	}
	skills := map[string]string{"skills/a/SKILL.md": moreSkill("a")}
	tests := []struct {
		name   string
		fsys   fstest.MapFS
		entry  domain.ClaudeEntry
		check  func(*testing.T, domain.Manifest)
		noSkip bool
	}{
		{"entry fills empty manifest fields", withFiles(fstest.MapFS{".claude-plugin/plugin.json": {Data: []byte(`{}`)}}, skills), entry,
			func(t *testing.T, m domain.Manifest) {
				if m.Name != "from-entry" || m.Version != "3.0.0" || m.Description != "Entry description." ||
					m.License != "Apache-2.0" || m.Homepage != "https://entry.example" || m.Author.Name != "Entry Author" {
					t.Errorf("manifest = %+v", m)
				}
			}, false},
		{"manifest wins over the entry", withFiles(morePlugin(`"version":"1.0.0","description":"Own."`), skills), entry,
			func(t *testing.T, m domain.Manifest) {
				if m.Name != "more-kit" || m.Version != "1.0.0" || m.Description != "Own." {
					t.Errorf("manifest = %+v", m)
				}
			}, false},
		{"description from the README heading", withFiles(morePlugin(``), map[string]string{
			"skills/a/SKILL.md": moreSkill("a"), "README.md": "\n# Heading line\nSecond."}), domain.ClaudeEntry{},
			func(t *testing.T, m domain.Manifest) {
				if m.Description != "Heading line" {
					t.Errorf("description = %q", m.Description)
				}
			}, false},
		{"README badges and markup lines are skipped", withFiles(morePlugin(``), map[string]string{
			"skills/a/SKILL.md": moreSkill("a"), "README.md": "<p>html</p>\n[![b](x)](y)\n\nReal text   here."}), domain.ClaudeEntry{},
			func(t *testing.T, m domain.Manifest) {
				if m.Description != "Real text here." {
					t.Errorf("description = %q", m.Description)
				}
			}, false},
		{"lower-case readme is read", withFiles(morePlugin(``), map[string]string{
			"skills/a/SKILL.md": moreSkill("a"), "readme.md": "From lower case"}), domain.ClaudeEntry{},
			func(t *testing.T, m domain.Manifest) {
				if m.Description != "From lower case" {
					t.Errorf("description = %q", m.Description)
				}
			}, false},
		{"no description and no README uses the name", withFiles(morePlugin(``), skills), domain.ClaudeEntry{},
			func(t *testing.T, m domain.Manifest) {
				if m.Description != "more-kit" {
					t.Errorf("description = %q", m.Description)
				}
			}, false},
		{"a README with only markup falls back to the name", withFiles(morePlugin(``), map[string]string{
			"skills/a/SKILL.md": moreSkill("a"), "README.md": "<p>x</p>\n"}), domain.ClaudeEntry{},
			func(t *testing.T, m domain.Manifest) {
				if m.Description != "more-kit" {
					t.Errorf("description = %q", m.Description)
				}
			}, false},
		{"long multi-byte description is cut at 255 characters", withFiles(morePlugin(`"description":"`+strings.Repeat("é", 300)+`"`), skills), domain.ClaudeEntry{},
			func(t *testing.T, m domain.Manifest) {
				if n := utf8.RuneCountInString(m.Description); n != 255 {
					t.Errorf("description has %d runes, want 255", n)
				}
			}, false},
		{"a cut that ends on a space is trimmed", withFiles(morePlugin(`"description":"`+strings.Repeat("a", 254)+` bbbb"`), skills), domain.ClaudeEntry{},
			func(t *testing.T, m domain.Manifest) {
				if m.Description != strings.Repeat("a", 254) {
					t.Errorf("description has %d characters, want the 254 a's", len(m.Description))
				}
			}, false},
		{"licence that is not SPDX becomes NOASSERTION", withFiles(morePlugin(`"license":"see file; really"`), skills), domain.ClaudeEntry{},
			func(t *testing.T, m domain.Manifest) {
				if m.License != "NOASSERTION" {
					t.Errorf("license = %q", m.License)
				}
			}, false},
		{"licence expression is kept", withFiles(morePlugin(`"license":"Apache-2.0 OR MIT"`), skills), domain.ClaudeEntry{},
			func(t *testing.T, m domain.Manifest) {
				if m.License != "Apache-2.0 OR MIT" {
					t.Errorf("license = %q", m.License)
				}
			}, false},
		{"long author name is cut at 128", withFiles(morePlugin(`"author":"`+strings.Repeat("n", 200)+`"`), skills), domain.ClaudeEntry{},
			func(t *testing.T, m domain.Manifest) {
				if len(m.Author.Name) != 128 {
					t.Errorf("author name has %d characters, want 128", len(m.Author.Name))
				}
			}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := domain.NormalizeClaudePlugin(tt.fsys, tt.entry, "ref", "")
			if err != nil {
				t.Fatalf("NormalizeClaudePlugin() error = %v", err)
			}
			tt.check(t, got.Manifest)
		})
	}
}

func TestNormalizeClaudePlugin_ReportsWhatItDoesNotMap(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		extra string
		files map[string]string
		entry domain.ClaudeEntry
		want  []string
		deny  []string
	}{
		{"unmapped manifest keys", `"lspServers":{},"outputStyles":"x","workflows":[],"channels":[],"userConfig":{},"experimental":{}`, nil, domain.ClaudeEntry{},
			[]string{"lspServers", "outputStyles", "workflows", "channels", "userConfig", "experimental"}, nil},
		{"hooks folder file", ``, map[string]string{"hooks/hooks.json": `{}`}, domain.ClaudeEntry{}, []string{"hooks"}, nil},
		{"commands folder", ``, map[string]string{"commands/x.md": "x"}, domain.ClaudeEntry{}, []string{"commands"}, nil},
		{"hooks file beside a marketplace entry is not read", ``, map[string]string{"hooks/hooks.json": `{}`}, domain.ClaudeEntry{Name: "e", Raw: json.RawMessage(`{"name":"e","skills":"skills"}`)}, nil, []string{"hooks"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			files := map[string]string{"skills/a/SKILL.md": moreSkill("a")}
			for k, v := range tt.files {
				files[k] = v
			}
			fsys := withFiles(morePlugin(tt.extra), files)
			if tt.entry.Name != "" {
				delete(fsys, ".claude-plugin/plugin.json")
			}
			got, err := domain.NormalizeClaudePlugin(fsys, tt.entry, "ref", "")
			if err != nil {
				t.Fatalf("NormalizeClaudePlugin() error = %v", err)
			}
			for _, w := range tt.want {
				if !reportMentions(got.Report.NotMapped, w) {
					t.Errorf("NotMapped = %v, want a mention of %q", got.Report.NotMapped, w)
				}
			}
			for _, d := range tt.deny {
				if reportMentions(got.Report.NotMapped, d) {
					t.Errorf("NotMapped = %v, want no mention of %q", got.Report.NotMapped, d)
				}
			}
		})
	}
}

func TestParseMarketplace_MoreRefusals(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		raw  string
	}{
		{"plugin entry is not an object", `{"name":"m","owner":{"name":"o"},"plugins":[5]}`},
		{"source of the wrong type", `{"name":"m","owner":{"name":"o"},"plugins":[{"name":"p","source":5}]}`},
		{"source type not known", `{"name":"m","owner":{"name":"o"},"plugins":[{"name":"p","source":{"source":"ftp"}}]}`},
		{"owner of the wrong type", `{"name":"m","owner":5,"plugins":[]}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if _, err := domain.ParseMarketplace([]byte(tt.raw)); !errors.Is(err, domain.ErrInvalidManifest) {
				t.Fatalf("ParseMarketplace() error = %v, want ErrInvalidManifest", err)
			}
		})
	}
}

func TestSanitizeName(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in, want string
	}{
		{"", "plugin"},
		{"---", "plugin"},
		{"Hello World!", "hello-world"},
		{"under_score", "under-score"},
		{"--lead-and-trail--", "lead-and-trail"},
		{"123abc", "p-123abc"},
		{"9", "p-9"},
		{strings.Repeat("a", 63) + "-bbbb", strings.Repeat("a", 63)},
		{strings.Repeat("a", 100), strings.Repeat("a", 64)},
		{"ÉCOLE", "cole"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			t.Parallel()
			if got := domain.SanitizeName(tt.in); got != tt.want {
				t.Errorf("SanitizeName(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestNormalizeAgentSkills_ReportsNotesAndSkipsWhatItCannotUse(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"skills/alpha/SKILL.md":   {Data: []byte("---\nname: alpha\ndescription: Alpha skill.\ncompatibility: needs   a\n  shell\nallowed-tools: Bash\n---\nBody")},
		"skills/.hidden/SKILL.md": {Data: []byte(moreSkill("hidden"))},
		"beta/SKILL.md":           {Data: []byte(moreSkill("beta"))},
		"bad/SKILL.md":            {Data: []byte("no frontmatter")},
		"mismatch/SKILL.md":       {Data: []byte(moreSkill("other"))},
		"plain/README.md":         {Data: []byte("no skill here")},
	}
	got, err := domain.NormalizeAgentSkills(fsys, "My Repo", "ref")
	if err != nil {
		t.Fatalf("NormalizeAgentSkills() error = %v", err)
	}
	if got.Manifest.Name != "my-repo" || !strings.HasPrefix(got.Manifest.Description, "2 skills from") {
		t.Errorf("manifest = %+v", got.Manifest)
	}
	var paths []string
	for _, s := range got.Manifest.Contents.Skills {
		paths = append(paths, s.Path)
	}
	if !equalStrings(paths, []string{"./beta", "./skills/alpha"}) {
		t.Errorf("skill paths = %v", paths)
	}
	for _, w := range []string{"bad", "other", "allowed-tools"} {
		if !reportMentions(got.Report.NotMapped, w) {
			t.Errorf("NotMapped = %v, want a mention of %q", got.Report.NotMapped, w)
		}
	}
	if !reportMentions(got.Report.Notes, "needs a shell") {
		t.Errorf("Notes = %v, want the compatibility text on one line", got.Report.Notes)
	}
}

func TestNormalizeAgentSkills_ASingleSkillTakesItsOwnMetadata(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		front       string
		wantVersion string
		wantLicense string
	}{
		{"valid version and licence", "name: solo\ndescription: Solo.\nlicense: Apache-2.0\nmetadata:\n  version: 2.4.6", "2.4.6", "Apache-2.0"},
		{"version that is not semantic is ignored", "name: solo\ndescription: Solo.\nmetadata:\n  version: two", "0.0.0", "NOASSERTION"},
		{"no metadata", "name: solo\ndescription: Solo.", "0.0.0", "NOASSERTION"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			fsys := fstest.MapFS{"SKILL.md": {Data: []byte("---\n" + tt.front + "\n---\nBody")}}
			got, err := domain.NormalizeAgentSkills(fsys, "repo", "ref")
			if err != nil {
				t.Fatalf("NormalizeAgentSkills() error = %v", err)
			}
			if got.Manifest.Name != "solo" || got.TreePrefix != "skills/solo" ||
				got.Manifest.Version != tt.wantVersion || got.Manifest.License != tt.wantLicense {
				t.Errorf("result = %+v prefix %q", got.Manifest, got.TreePrefix)
			}
		})
	}
}

func TestNormalizeRegistryServer_MoreMappings(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		change      func(*domain.RegistryServer)
		opt         domain.RegistryOptions
		wantServers int
		wantAuthor  string
		wantHome    string
		wantNotMaps string
		wantErr     error
	}{
		{
			name:        "name without a namespace gives an unknown publisher",
			change:      func(s *domain.RegistryServer) { s.Name = "plain-name" },
			wantServers: 1, wantAuthor: "Unknown", wantHome: "https://acme.example/weather",
		},
		{
			name:        "empty namespace gives an unknown publisher",
			change:      func(s *domain.RegistryServer) { s.Name = "/plain" },
			wantServers: 1, wantAuthor: "Unknown", wantHome: "https://acme.example/weather",
		},
		{
			name: "homepage falls back to the repository",
			change: func(s *domain.RegistryServer) {
				s.WebsiteURL = ""
				s.Repository.URL = "https://github.com/acme/weather"
			},
			wantServers: 1, wantAuthor: "io.github.Acme", wantHome: "https://github.com/acme/weather",
		},
		{
			name: "sse remote is reported and skipped",
			change: func(s *domain.RegistryServer) {
				s.Remotes = []domain.RegistryRemote{{Type: "sse", URL: "https://r.example/sse"}}
			},
			wantServers: 1, wantAuthor: "io.github.Acme", wantHome: "https://acme.example/weather", wantNotMaps: "sse",
		},
		{
			name: "remote headers are mapped, secrets become settings and bad names are reported",
			change: func(s *domain.RegistryServer) {
				s.Remotes = []domain.RegistryRemote{{Type: "streamable-http", URL: "https://r.example/mcp", Headers: []domain.RegistryHeader{
					{Name: "X-Client", Value: "agentsone"},
					{Name: "X-Api-Key", IsSecret: true, Value: "must-not-appear"},
					{Name: "bad header", Value: "x"},
					{Name: "X-Empty"},
				}}}
			},
			wantServers: 2, wantAuthor: "io.github.Acme", wantHome: "https://acme.example/weather", wantNotMaps: "bad header",
		},
		{
			name: "remote without headers",
			change: func(s *domain.RegistryServer) {
				s.Packages = nil
				s.Remotes = []domain.RegistryRemote{{Type: "streamable-http", URL: "https://r.example/mcp"}}
			},
			wantServers: 1, wantAuthor: "io.github.Acme", wantHome: "https://acme.example/weather",
		},
		{
			name: "packages that cannot run are reported",
			change: func(s *domain.RegistryServer) {
				s.Packages = append(s.Packages,
					domain.RegistryPackage{RegistryType: "oci", Identifier: "img", Transport: domain.RegistryTransport{Type: "stdio"}},
					domain.RegistryPackage{RegistryType: "nuget", Identifier: "n", Transport: domain.RegistryTransport{Type: "stdio"}},
					domain.RegistryPackage{RegistryType: "npm", Identifier: "h", Transport: domain.RegistryTransport{Type: "streamable-http"}},
				)
			},
			wantServers: 1, wantAuthor: "io.github.Acme", wantHome: "https://acme.example/weather", wantNotMaps: "container engine",
		},
		{
			name: "oci package runs with a container engine",
			change: func(s *domain.RegistryServer) {
				s.Packages = []domain.RegistryPackage{{
					RegistryType: "oci", Identifier: "ghcr.io/acme/w:1", Transport: domain.RegistryTransport{Type: "stdio"},
					EnvironmentVariables: []domain.RegistryEnv{{Name: "REGION", Value: "eu"}, {Name: "bad-env", Value: "x"}},
				}}
			},
			opt:         domain.RegistryOptions{ContainerEngine: true},
			wantServers: 1, wantAuthor: "io.github.Acme", wantHome: "https://acme.example/weather", wantNotMaps: "bad-env",
		},
		{
			name: "several packages of one type get unique names",
			change: func(s *domain.RegistryServer) {
				p := s.Packages[0]
				s.Packages = []domain.RegistryPackage{p, p, p}
			},
			wantServers: 3, wantAuthor: "io.github.Acme", wantHome: "https://acme.example/weather",
		},
		{
			name:    "version that is not semantic is refused",
			change:  func(s *domain.RegistryServer) { s.Version = "latest" },
			wantErr: domain.ErrNotSemantic,
		},
		{
			name:    "nothing runnable is refused",
			change:  func(s *domain.RegistryServer) { s.Packages = nil },
			wantErr: domain.ErrNothingToInstall,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := registryServer()
			tt.change(&s)
			got, err := domain.NormalizeRegistryServer(s, tt.opt)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("error = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("NormalizeRegistryServer() error = %v", err)
			}
			servers := got.Manifest.Contents.McpServers
			if len(servers) != tt.wantServers {
				t.Fatalf("servers = %d, want %d", len(servers), tt.wantServers)
			}
			names := map[string]bool{}
			for _, srv := range servers {
				if names[srv.Name] {
					t.Errorf("server name %q is used twice", srv.Name)
				}
				names[srv.Name] = true
			}
			if got.Manifest.Author.Name != tt.wantAuthor || got.Manifest.Homepage != tt.wantHome {
				t.Errorf("author %q homepage %q, want %q and %q", got.Manifest.Author.Name, got.Manifest.Homepage, tt.wantAuthor, tt.wantHome)
			}
			if tt.wantNotMaps != "" && !reportMentions(got.Report.NotMapped, tt.wantNotMaps) {
				t.Errorf("NotMapped = %v, want a mention of %q", got.Report.NotMapped, tt.wantNotMaps)
			}
			raw, err := json.Marshal(got.Manifest)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(raw), "must-not-appear") {
				t.Errorf("a secret header value reached the manifest: %s", raw)
			}
		})
	}
}

func TestNormalizeRegistryServer_SecretHeaderBecomesAUserSetting(t *testing.T) {
	t.Parallel()
	s := domain.RegistryServer{
		Name: "io.example/svc", Version: "1.0.0", Description: "Svc.",
		Remotes: []domain.RegistryRemote{{Type: "streamable-http", URL: "https://r.example/mcp", Headers: []domain.RegistryHeader{
			{Name: "X-Api-Key", IsSecret: true},
			{Name: "X-Plain", Value: "v"},
		}}},
	}
	got, err := domain.NormalizeRegistryServer(s, domain.RegistryOptions{})
	if err != nil {
		t.Fatalf("NormalizeRegistryServer() error = %v", err)
	}
	h := got.Manifest.Contents.McpServers[0].Headers
	if h["X-Api-Key"] != "${user_config.X_API_KEY}" || h["X-Plain"] != "v" {
		t.Errorf("headers = %v", h)
	}
	if !reportMentions(got.Report.Notes, "X-Api-Key") {
		t.Errorf("Notes = %v, want a mention of the secret header", got.Report.Notes)
	}
}
