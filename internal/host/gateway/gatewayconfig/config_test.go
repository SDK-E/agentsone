// SPDX-License-Identifier: MIT

package gatewayconfig_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SDK-E/agentsone/internal/host/gateway/gatewayconfig"
	"github.com/SDK-E/agentsone/internal/host/gateway/routing"
)

func TestDefaultsAreValidAndAllDisabled(t *testing.T) {
	f := gatewayconfig.Default()
	cat, err := f.Catalog()
	if err != nil {
		t.Fatalf("Catalog: %v", err)
	}
	want := []routing.ProviderID{"anthropic", "openai", "openrouter", "gemini", "mistral", "groq", "custom"}
	if len(cat.Providers) != len(want) {
		t.Fatalf("providers = %d, want %d", len(cat.Providers), len(want))
	}
	for i, p := range cat.Providers {
		if p.ID != want[i] || p.Enabled {
			t.Fatalf("provider %d = %+v; want %s, disabled", i, p, want[i])
		}
	}
	anth, _ := cat.Provider("anthropic")
	if !anth.Supports(routing.APIAnthropic) || !anth.Supports(routing.APIOpenAI) {
		t.Fatalf("anthropic APIs = %v", anth.APIs)
	}
	// Every tier has a Claude model, so the Claude engine always has a route.
	for _, tier := range []routing.Weight{routing.Quick, routing.Normal, routing.Deep} {
		found := false
		for _, m := range cat.Models {
			if m.Provider == "anthropic" && m.Tier == tier && m.IsClaude() {
				found = true
			}
		}
		if !found {
			t.Fatalf("no default Claude model in tier %s", tier)
		}
	}
	cfgs := f.ProviderConfigs()
	if cfgs[1].ID != "openai" || cfgs[1].MaxTokensField != "max_completion_tokens" || !cfgs[1].KeyRequired {
		t.Fatalf("openai config = %+v", cfgs[1])
	}
	if cfgs[6].ID != "custom" || cfgs[6].KeyRequired {
		t.Fatalf("custom config = %+v", cfgs[6])
	}
	l := f.Limits
	if l.FlushIntervalSeconds != 5 || l.RetryAfterSeconds <= 0 || l.DefaultMaxOutputTokens <= 0 {
		t.Fatalf("limits = %+v", l)
	}
}

func TestLoadMissingFileGivesDefaults(t *testing.T) {
	f, err := gatewayconfig.Load(filepath.Join(t.TempDir(), "gateway.toml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(f.Providers) != len(gatewayconfig.Default().Providers) {
		t.Fatal("missing file did not give the defaults")
	}
}

func TestSaveLoadRoundTripAndEnable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.toml")
	f := gatewayconfig.Default()
	if err := f.SetEnabled("groq", true); err != nil {
		t.Fatalf("SetEnabled: %v", err)
	}
	if err := f.SetEnabled("nope", true); !errors.Is(err, gatewayconfig.ErrUnknownProvider) {
		t.Fatalf("SetEnabled(nope) error = %v", err)
	}
	if err := gatewayconfig.Save(path, f); err != nil {
		t.Fatalf("Save: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("file mode = %v, %v", info.Mode(), err)
	}
	raw, _ := os.ReadFile(path) //nolint:gosec // G304: the path is inside t.TempDir.
	if !strings.Contains(string(raw), "input_usd_per_mtok") {
		t.Fatalf("saved file is not human-readable TOML:\n%s", raw)
	}
	again, err := gatewayconfig.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	cat, err := again.Catalog()
	if err != nil {
		t.Fatalf("Catalog: %v", err)
	}
	g, _ := cat.Provider("groq")
	if !g.Enabled {
		t.Fatal("groq not enabled after reload")
	}
}

func TestHumanEditedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.toml")
	text := `
[limits]
flush_interval_seconds = 2

[[provider]]
id = "custom"
enabled = true
openai_base_url = "http://127.0.0.1:11434/v1"

  [[provider.model]]
  id = "llama3.2"
  tier = "normal"
  family = "llama"
  input_usd_per_mtok = "0"
  output_usd_per_mtok = "0"
`
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := gatewayconfig.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	cat, err := f.Catalog()
	if err != nil {
		t.Fatalf("Catalog: %v", err)
	}
	if len(cat.Providers) != 1 || len(cat.Models) != 1 || cat.Models[0].ID != "llama3.2" {
		t.Fatalf("catalog = %+v", cat)
	}
	// Unset limits keep their defaults.
	if f.Limits.FlushIntervalSeconds != 2 || f.Limits.RetryAfterSeconds != gatewayconfig.Default().Limits.RetryAfterSeconds {
		t.Fatalf("limits = %+v", f.Limits)
	}
}

func TestBadFilesAreRefused(t *testing.T) {
	tests := map[string]string{
		"not toml":         "[[provider",
		"unknown field":    "[[provider]]\nid = \"x\"\nopenai_base_url = \"http://x\"\ncolour = \"red\"\n",
		"bad price":        "[[provider]]\nid = \"x\"\nopenai_base_url = \"http://x\"\n[[provider.model]]\nid = \"m\"\ntier = \"quick\"\ninput_usd_per_mtok = \"cheap\"\noutput_usd_per_mtok = \"0\"\n",
		"bad tier":         "[[provider]]\nid = \"x\"\nopenai_base_url = \"http://x\"\n[[provider.model]]\nid = \"m\"\ntier = \"huge\"\ninput_usd_per_mtok = \"0\"\noutput_usd_per_mtok = \"0\"\n",
		"no API":           "[[provider]]\nid = \"x\"\n",
		"bad URL":          "[[provider]]\nid = \"x\"\nopenai_base_url = \"ftp://x\"\n",
		"bad output field": "[[provider]]\nid = \"x\"\nopenai_base_url = \"http://x\"\nmax_tokens_field = \"limit\"\n",
	}
	for name, text := range tests {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "gateway.toml")
			if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := gatewayconfig.Load(path); err == nil {
				t.Fatalf("Load accepted %q", text)
			}
		})
	}
}

func TestSaveFailsInMissingDirectory(t *testing.T) {
	if err := gatewayconfig.Save(filepath.Join(t.TempDir(), "no", "such", "gateway.toml"), gatewayconfig.Default()); err == nil {
		t.Fatal("Save succeeded in a missing directory")
	}
}
