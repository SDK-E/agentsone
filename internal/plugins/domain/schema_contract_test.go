// SPDX-License-Identifier: MIT

package domain_test

import (
	"testing"
	"testing/fstest"

	"github.com/SDK-E/agentsone/internal/contract/contracttest"
	"github.com/SDK-E/agentsone/internal/plugins/domain"
)

func schemaSet(t *testing.T) *contracttest.SchemaSet {
	t.Helper()
	set, err := contracttest.NewSchemaSet("../../../contract/schemas")
	if err != nil {
		t.Fatalf("load schemas: %v", err)
	}
	return set
}

// Every source adapter must produce a manifest the contract schema accepts.
func TestNormalizers_ProduceManifestsThatMatchTheContractSchema(t *testing.T) {
	t.Parallel()
	set := schemaSet(t)

	claude, err := domain.NormalizeClaudePlugin(claudeTree(), domain.ClaudeEntry{}, "example-marketplace/garden-kit", "")
	if err != nil {
		t.Fatal(err)
	}
	skills, err := domain.NormalizeAgentSkills(fstest.MapFS{
		"skills/pdf-tools/SKILL.md": {Data: []byte(skillMD)},
	}, "my-skills", "https://example.com/skills.git")
	if err != nil {
		t.Fatal(err)
	}
	registry, err := domain.NormalizeRegistryServer(registryServer(), domain.RegistryOptions{})
	if err != nil {
		t.Fatal(err)
	}

	tests := map[string]domain.Normalized{"claude-code": claude, "agent-skills": skills, "mcp-registry": registry}
	for name, n := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			raw, err := n.Manifest.Encode()
			if err != nil {
				t.Fatalf("Encode() error = %v", err)
			}
			if err := set.Validate("plugin-manifest", raw); err != nil {
				t.Errorf("manifest does not match the schema: %v\n%s", err, raw)
			}
		})
	}
}

// Agent templates generated from Claude agent Markdown must match the
// agent-template schema.
func TestNormalizeClaudePlugin_GeneratedTemplatesMatchTheContractSchema(t *testing.T) {
	t.Parallel()
	set := schemaSet(t)
	got, err := domain.NormalizeClaudePlugin(claudeTree(), domain.ClaudeEntry{}, "m/garden-kit", "")
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, f := range got.Files {
		if len(f.Path) > len("agentsone/templates/") && f.Path[:len("agentsone/templates/")] == "agentsone/templates/" {
			found++
			if err := set.Validate("agent-template", f.Data); err != nil {
				t.Errorf("%s does not match the schema: %v\n%s", f.Path, err, f.Data)
			}
		}
	}
	if found == 0 {
		t.Fatal("no generated template files")
	}
}
