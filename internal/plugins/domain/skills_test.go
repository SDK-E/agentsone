// SPDX-License-Identifier: MIT

package domain_test

import (
	"errors"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/SDK-E/agentsone/internal/plugins/domain"
)

const skillMD = `---
name: pdf-tools
description: Fill and read PDF forms.
license: Apache-2.0
metadata:
  version: "1.4.0"
---
# PDF tools

Use this skill to work with PDFs.
`

func TestParseSkillMD_ReadsFrontmatterAndBody(t *testing.T) {
	t.Parallel()
	doc, err := domain.ParseSkillMD([]byte(skillMD))
	if err != nil {
		t.Fatalf("ParseSkillMD() error = %v", err)
	}
	if doc.Name != "pdf-tools" || doc.Description != "Fill and read PDF forms." || doc.License != "Apache-2.0" {
		t.Errorf("doc = %+v", doc)
	}
	if doc.Metadata["version"] != "1.4.0" {
		t.Errorf("metadata = %v", doc.Metadata)
	}
	if !strings.HasPrefix(doc.Body, "# PDF tools") {
		t.Errorf("body = %q", doc.Body)
	}
}

func TestParseSkillMD_Refusals(t *testing.T) {
	t.Parallel()
	tests := map[string]string{
		"no frontmatter":         "# just text",
		"unterminated":           "---\nname: a\n",
		"missing name":           "---\ndescription: x\n---\nbody",
		"missing description":    "---\nname: a\n---\nbody",
		"uppercase name":         "---\nname: Pdf\ndescription: x\n---\n",
		"double hyphen":          "---\nname: a--b\ndescription: x\n---\n",
		"leading hyphen":         "---\nname: -a\ndescription: x\n---\n",
		"name over 64":           "---\nname: " + strings.Repeat("a", 65) + "\ndescription: x\n---\n",
		"description over 1024":  "---\nname: a\ndescription: " + strings.Repeat("x", 1025) + "\n---\n",
		"compatibility over 500": "---\nname: a\ndescription: x\ncompatibility: " + strings.Repeat("x", 501) + "\n---\n",
	}
	for name, in := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := domain.ParseSkillMD([]byte(in)); !errors.Is(err, domain.ErrInvalidManifest) {
				t.Fatalf("ParseSkillMD() error = %v, want ErrInvalidManifest", err)
			}
		})
	}
}

func TestNormalizeAgentSkills_ManySkillsBecomeOnePluginNamedAfterTheRepository(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"skills/pdf-tools/SKILL.md": {Data: []byte(skillMD)},
		"skills/notes/SKILL.md":     {Data: []byte("---\nname: notes\ndescription: Take notes.\n---\nBody")},
		"README.md":                 {Data: []byte("# Skills\n")},
	}

	got, err := domain.NormalizeAgentSkills(fsys, "My_Skills", "https://example.com/skills.git")
	if err != nil {
		t.Fatalf("NormalizeAgentSkills() error = %v", err)
	}

	m := got.Manifest
	if m.Name != "my-skills" || m.Source.Kind != domain.SourceAgentSkills || m.Source.Ref != "https://example.com/skills.git" {
		t.Errorf("manifest header = %+v", m)
	}
	if m.Version != "0.0.0" || m.License != "NOASSERTION" {
		t.Errorf("version %q license %q, want 0.0.0 and NOASSERTION", m.Version, m.License)
	}
	if len(m.Contents.Skills) != 2 || m.Contents.Skills[0].Name != "notes" || m.Contents.Skills[1].Path != "./skills/pdf-tools" {
		t.Errorf("skills = %+v", m.Contents.Skills)
	}
	if err := m.Validate(); err != nil {
		t.Errorf("normalised manifest invalid: %v", err)
	}
	if got.TreePrefix != "" {
		t.Errorf("TreePrefix = %q, want empty", got.TreePrefix)
	}
}

func TestNormalizeAgentSkills_ASkillAtTheRootIsMovedIntoSkillsFolder(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{"SKILL.md": {Data: []byte(skillMD)}}

	got, err := domain.NormalizeAgentSkills(fsys, "whatever", "./pdf-tools")
	if err != nil {
		t.Fatalf("NormalizeAgentSkills() error = %v", err)
	}

	if got.TreePrefix != "skills/pdf-tools" {
		t.Errorf("TreePrefix = %q, want skills/pdf-tools", got.TreePrefix)
	}
	m := got.Manifest
	if m.Name != "pdf-tools" || m.Version != "1.4.0" || m.License != "Apache-2.0" || m.Description != "Fill and read PDF forms." {
		t.Errorf("manifest = %+v", m)
	}
	if err := m.Validate(); err != nil {
		t.Errorf("normalised manifest invalid: %v", err)
	}
}

func TestNormalizeAgentSkills_SkillWhoseNameDiffersFromItsDirectoryIsReportedAndLeftOut(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"skills/good/SKILL.md":  {Data: []byte("---\nname: good\ndescription: ok\n---\n")},
		"skills/wrong/SKILL.md": {Data: []byte("---\nname: other\ndescription: ok\n---\n")},
	}

	got, err := domain.NormalizeAgentSkills(fsys, "repo", "ref")
	if err != nil {
		t.Fatalf("NormalizeAgentSkills() error = %v", err)
	}

	if len(got.Manifest.Contents.Skills) != 1 || got.Manifest.Contents.Skills[0].Name != "good" {
		t.Fatalf("skills = %+v", got.Manifest.Contents.Skills)
	}
	if len(got.Report.NotMapped) != 1 || !strings.Contains(got.Report.NotMapped[0], "wrong") {
		t.Errorf("report = %+v, want the wrong skill named", got.Report)
	}
}

func TestNormalizeAgentSkills_NoSkillIsNothingToInstall(t *testing.T) {
	t.Parallel()
	_, err := domain.NormalizeAgentSkills(fstest.MapFS{"README.md": {Data: []byte("x")}}, "repo", "ref")
	if !errors.Is(err, domain.ErrNothingToInstall) {
		t.Fatalf("error = %v, want ErrNothingToInstall", err)
	}
}

func TestNormalizeAgentSkills_LicenceThatIsNotSPDXBecomesNoAssertion(t *testing.T) {
	t.Parallel()
	md := "---\nname: a\ndescription: x\nlicense: Proprietary. LICENSE.txt has complete terms\n---\n"
	got, err := domain.NormalizeAgentSkills(fstest.MapFS{"SKILL.md": {Data: []byte(md)}}, "a", "ref")
	if err != nil {
		t.Fatal(err)
	}
	if got.Manifest.License != "NOASSERTION" {
		t.Fatalf("license = %q, want NOASSERTION", got.Manifest.License)
	}
}
