// SPDX-License-Identifier: MIT

package domain

import (
	"bytes"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
	"unicode/utf8"

	"go.yaml.in/yaml/v3"
)

// SkillDoc is a parsed SKILL.md.
type SkillDoc struct {
	Name          string            `yaml:"name"`
	Description   string            `yaml:"description"`
	License       string            `yaml:"license"`
	Compatibility string            `yaml:"compatibility"`
	Metadata      map[string]string `yaml:"metadata"`
	AllowedTools  string            `yaml:"allowed-tools"`
	// Body is the Markdown after the frontmatter.
	Body string `yaml:"-"`
}

// ParseSkillMD reads the YAML frontmatter and body of a SKILL.md and checks
// the Agent Skills rules for name, description and compatibility.
func ParseSkillMD(raw []byte) (SkillDoc, error) {
	front, body, ok := splitFrontmatter(raw)
	if !ok {
		return SkillDoc{}, errorSentinelFn("SKILL.md has no YAML frontmatter")
	}
	var doc SkillDoc
	if err := yaml.Unmarshal(front, &doc); err != nil {
		return SkillDoc{}, errorSentinelFn("SKILL.md frontmatter: %v", err)
	}
	doc.Body = string(body)
	if len(doc.Name) > 64 || !skillNameRe.MatchString(doc.Name) {
		return SkillDoc{}, errorSentinelFn("skill name %q must be 1 to 64 lowercase letters, digits and single hyphens", doc.Name)
	}
	if n := utf8.RuneCountInString(doc.Description); n < 1 || n > 1024 {
		return SkillDoc{}, errorSentinelFn("skill %q description must be 1 to 1024 characters", doc.Name)
	}
	if utf8.RuneCountInString(doc.Compatibility) > 500 {
		return SkillDoc{}, errorSentinelFn("skill %q compatibility is over 500 characters", doc.Name)
	}
	return doc, nil
}

// splitFrontmatter cuts "---\n<yaml>\n---\n<body>".
func splitFrontmatter(raw []byte) (front, body []byte, ok bool) {
	raw = bytes.TrimPrefix(raw, []byte("\xef\xbb\xbf"))
	text := strings.ReplaceAll(string(raw), "\r\n", "\n")
	rest, found := strings.CutPrefix(text, "---\n")
	if !found {
		return nil, nil, false
	}
	front2, after, found := strings.Cut(rest, "\n---")
	if !found {
		return nil, nil, false
	}
	after = strings.TrimPrefix(after, "\n")
	return []byte(front2), []byte(after), true
}

// skillDirs finds skill directories in a fetched tree: the root itself, or
// the folders one level below the root and below skills/. It returns
// directory paths ("." for the root) in sorted order.
func skillDirs(fsys fs.FS) []string {
	if exists(fsys, "SKILL.md") {
		return []string{"."}
	}
	var dirs []string
	for _, base := range []string{".", "skills"} {
		entries, err := fs.ReadDir(fsys, base)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
				continue
			}
			dir := path.Join(base, e.Name())
			if exists(fsys, path.Join(dir, "SKILL.md")) {
				dirs = append(dirs, dir)
			}
		}
	}
	sort.Strings(dirs)
	return dirs
}

func exists(fsys fs.FS, name string) bool {
	info, err := fs.Stat(fsys, name)
	return err == nil && !info.IsDir()
}

// NormalizeAgentSkills turns a tree of Agent Skills into one plugin. A tree
// holding one skill at its root becomes the plugin of that skill, moved to
// skills/<name>. A tree holding several skill directories becomes one plugin
// named after repo. ref is recorded as source.ref.
func NormalizeAgentSkills(fsys fs.FS, repo, ref string) (Normalized, error) {
	dirs := skillDirs(fsys)
	var out Normalized
	var skills []Skill
	var docs []SkillDoc
	for _, dir := range dirs {
		raw, err := fs.ReadFile(fsys, path.Join(dir, "SKILL.md"))
		if err != nil {
			return Normalized{}, fmt.Errorf("read %s/SKILL.md: %w", dir, err)
		}
		doc, err := ParseSkillMD(raw)
		if err != nil {
			out.Report.skip("skill in %q: %v", dir, err)
			continue
		}
		if dir == "." {
			out.TreePrefix = "skills/" + doc.Name
			skills = append(skills, Skill{Name: doc.Name, Path: "./skills/" + doc.Name, Description: doc.Description})
			docs = append(docs, doc)
			continue
		}
		if path.Base(dir) != doc.Name {
			out.Report.skip("skill %q: name must equal its directory name %q", doc.Name, path.Base(dir))
			continue
		}
		skills = append(skills, Skill{Name: doc.Name, Path: "./" + dir, Description: doc.Description})
		docs = append(docs, doc)
	}
	if len(skills) == 0 {
		return Normalized{}, fmt.Errorf("no valid SKILL.md found: %w", ErrNothingToInstall)
	}
	for _, d := range docs {
		if d.Compatibility != "" {
			out.Report.note("skill %s compatibility: %s", d.Name, oneLine(d.Compatibility))
		}
		if d.AllowedTools != "" {
			out.Report.skip("skill %s allowed-tools: tools come from MCP servers", d.Name)
		}
	}
	m, err := skillsManifest(docs, skills, repo, ref)
	if err != nil {
		return Normalized{}, err
	}
	out.Manifest = m
	return out, nil
}

func skillsManifest(docs []SkillDoc, skills []Skill, repo, ref string) (Manifest, error) {
	m := Manifest{
		Name:    SanitizeName(repo),
		Version: defaultVersion,
		Author:  Author{Name: "Unknown"},
		License: noAssertion,
		Source:  Source{Kind: SourceAgentSkills, Ref: ref},
		Compat:  Compat{AgentsOne: DefaultCompat},
		Contents: Contents{
			Skills: skills,
		},
	}
	if len(docs) == 1 {
		m.Name = docs[0].Name
		m.Description = cut(oneLine(docs[0].Description), 255)
		m.License = licenseOrNone(docs[0].License)
		if v := docs[0].Metadata["version"]; IsSemver(v) {
			m.Version = v
		}
	} else {
		m.Description = cut(fmt.Sprintf("%d skills from %s", len(skills), repo), 255)
	}
	return m, m.Validate()
}
