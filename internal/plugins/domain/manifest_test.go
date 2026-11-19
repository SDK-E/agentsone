// SPDX-License-Identifier: MIT

package domain_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SDK-E/agentsone/internal/plugins/domain"
)

const fixtures = "../../../contract/fixtures/schemas/plugin-manifest"

func readFixtures(t *testing.T, dir string) map[string][]byte {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(fixtures, dir, "*.json"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no fixtures in %s: %v", dir, err)
	}
	out := map[string][]byte{}
	for _, f := range files {
		b, err := os.ReadFile(f) // #nosec G304 -- fixture path chosen by the test
		if err != nil {
			t.Fatal(err)
		}
		out[filepath.Base(f)] = b
	}
	return out
}

func TestParseManifest_AcceptsEveryValidFixture(t *testing.T) {
	t.Parallel()
	for name, raw := range readFixtures(t, "valid") {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := domain.ParseManifest(raw); err != nil {
				t.Fatalf("ParseManifest() error = %v", err)
			}
		})
	}
}

func TestParseManifest_RefusesEveryInvalidFixture(t *testing.T) {
	t.Parallel()
	for name, raw := range readFixtures(t, "invalid") {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := domain.ParseManifest(raw)
			if !errors.Is(err, domain.ErrInvalidManifest) {
				t.Fatalf("ParseManifest() error = %v, want ErrInvalidManifest", err)
			}
		})
	}
}

func TestManifest_RefusesAPluginWithNothingInIt(t *testing.T) {
	t.Parallel()
	m := validManifest()
	err := m.Validate()
	if err != nil {
		t.Fatalf("base manifest invalid: %v", err)
	}
	m.Contents = domain.Contents{}
	if err := m.Validate(); !errors.Is(err, domain.ErrEmptyPlugin) {
		t.Fatalf("Validate() error = %v, want ErrEmptyPlugin", err)
	}
}

func TestManifest_KindsListsEveryKindOnce(t *testing.T) {
	t.Parallel()
	m := validManifest()
	m.Contents.Skills = append(m.Contents.Skills, domain.Skill{Name: "b", Path: "./skills/b", Description: "b"})
	m.Contents.Themes = []domain.FileRef{{Path: "./theme.json"}}
	got := m.Kinds()
	want := []domain.Kind{domain.KindSkill, domain.KindTheme}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("Kinds() = %v, want %v", got, want)
	}
}

func TestManifest_EncodesEmptyListsAsArrays(t *testing.T) {
	t.Parallel()
	raw, err := validManifest().Encode()
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"mcpServers": []`, `"runners": []`, `"panels": []`, `"emitters": []`} {
		if !strings.Contains(string(raw), key) {
			t.Errorf("encoded manifest lacks %s:\n%s", key, raw)
		}
	}
	if _, err := domain.ParseManifest(raw); err != nil {
		t.Fatalf("round trip: %v", err)
	}
}

func TestManifest_Validate_RefusesBadFields(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		change func(*domain.Manifest)
	}{
		{"name with capital", func(m *domain.Manifest) { m.Name = "Bad" }},
		{"name too long", func(m *domain.Manifest) { m.Name = "a" + strings.Repeat("b", 64) }},
		{"version with v prefix", func(m *domain.Manifest) { m.Version = "v1.0.0" }},
		{"description with newline", func(m *domain.Manifest) { m.Description = "a\nb" }},
		{"description over 255", func(m *domain.Manifest) { m.Description = strings.Repeat("x", 256) }},
		{"empty license", func(m *domain.Manifest) { m.License = "" }},
		{"source kind unknown", func(m *domain.Manifest) { m.Source.Kind = "ftp" }},
		{"empty source ref", func(m *domain.Manifest) { m.Source.Ref = "" }},
		{"skill path leaves root", func(m *domain.Manifest) { m.Contents.Skills[0].Path = "./../x" }},
		{"skill path with dot segment", func(m *domain.Manifest) { m.Contents.Skills[0].Path = "./.git/x" }},
		{"skill path absolute", func(m *domain.Manifest) { m.Contents.Skills[0].Path = "/etc/x" }},
		{"skill name differs from directory", func(m *domain.Manifest) { m.Contents.Skills[0].Name = "other" }},
		{"duplicate skill", func(m *domain.Manifest) { m.Contents.Skills = append(m.Contents.Skills, m.Contents.Skills[0]) }},
		{"stdio server without command", func(m *domain.Manifest) {
			m.Contents.McpServers = []domain.McpServer{{Name: "x", Transport: "stdio"}}
		}},
		{"http server without url", func(m *domain.Manifest) {
			m.Contents.McpServers = []domain.McpServer{{Name: "x", Transport: "streamable-http"}}
		}},
		{"panel scope malformed", func(m *domain.Manifest) {
			m.Contents.Panels = []domain.Panel{{Name: "p", Title: "P", Icon: "./i.svg", Entry: "./i.html", Scopes: []string{"agents"}}}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			m := validManifest()
			tt.change(&m)
			if err := m.Validate(); !errors.Is(err, domain.ErrInvalidManifest) {
				t.Fatalf("Validate() error = %v, want ErrInvalidManifest", err)
			}
		})
	}
}

func validManifest() domain.Manifest {
	return domain.Manifest{
		Name:        "demo",
		Version:     "1.2.3",
		Description: "A demo plugin.",
		Author:      domain.Author{Name: "Someone"},
		License:     "MIT",
		Source:      domain.Source{Kind: domain.SourceAgentsOne, Ref: "./demo"},
		Compat:      domain.Compat{AgentsOne: ">=0.1.0"},
		Contents: domain.Contents{
			Skills: []domain.Skill{{Name: "a", Path: "./skills/a", Description: "A skill."}},
		},
	}
}
