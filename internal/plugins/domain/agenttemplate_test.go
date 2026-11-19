// SPDX-License-Identifier: MIT

package domain_test

import (
	"errors"
	"testing"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/plugins/domain"
)

const routerTemplate = `{"name": "router", "kind": "chat", "isRoot": false, "runner": {"name": "openai", "config": {}},
  "oneLine": "Routes work.", "charter": "", "goals": [], "skills": [], "mcpServers": [],
  "limits": {"cpuPercent": 50, "memoryMiB": 256, "tasks": 64}, "wake": {"subscriptions": []}, "schedules": []}`

func TestParseAgentTemplate_IDIsThePluginNameAndTheTemplateName(t *testing.T) {
	t.Parallel()

	got, err := domain.ParseAgentTemplate("small-seed", []byte(routerTemplate))

	if err != nil {
		t.Fatalf("ParseAgentTemplate() error = %v", err)
	}
	if got.ID != "small-seed/router" || got.Name != "router" || got.Kind != core.AgentChat || got.IsRoot || got.Runner != "openai" {
		t.Errorf("template = %+v", got)
	}
	if string(got.Raw) != routerTemplate {
		t.Errorf("raw = %s, want the document as installed", got.Raw)
	}
}

func TestParseAgentTemplate_RefusesABadDocument(t *testing.T) {
	t.Parallel()
	for name, doc := range map[string]string{
		"not json":     `{`,
		"bad name":     `{"name": "Router", "kind": "chat", "runner": {"name": "openai"}}`,
		"long name":    `{"name": "a-name-that-is-longer-than-a-linux-user", "kind": "chat", "runner": {"name": "openai"}}`,
		"unknown kind": `{"name": "router", "kind": "daemon", "runner": {"name": "openai"}}`,
		"no runner":    `{"name": "router", "kind": "chat", "runner": {"name": ""}}`,
	} {
		if _, err := domain.ParseAgentTemplate("small-seed", []byte(doc)); !errors.Is(err, domain.ErrInvalidManifest) {
			t.Errorf("%s: error = %v, want ErrInvalidManifest", name, err)
		}
	}
}
