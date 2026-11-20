// SPDX-License-Identifier: MIT

package config_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gatewayconfig "github.com/SDK-E/agentsone/internal/edge/gateway/adapters/config"
	routing "github.com/SDK-E/agentsone/internal/edge/gateway/domain"
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

// Claude 4.7 and later and GPT-5 refuse sampling fields at anything but their default; the
// defaults say so, and the provider client gets the lists by model.
func TestDefaultsDropTheFieldsTheModelsRefuse(t *testing.T) {
	cfgs := gatewayconfig.Default().ProviderConfigs()
	byID := map[routing.ProviderID]map[string][]string{}
	for _, c := range cfgs {
		byID[c.ID] = c.DropParams
	}
	for _, m := range []string{"claude-haiku-5-5", "claude-sonnet-5-5", "claude-opus-5-5"} {
		if got := byID["anthropic"][m]; strings.Join(got, ",") != "temperature,top_p,top_k" {
			t.Errorf("anthropic %s drops %v", m, got)
		}
	}
	for p, models := range map[routing.ProviderID][]string{
		"openai": {"gpt-5-nano", "gpt-5-mini", "gpt-5"}, "openrouter": {"openai/gpt-5-nano", "openai/gpt-5-mini", "openai/gpt-5"},
	} {
		for _, m := range models {
			if got := byID[p][m]; strings.Join(got, ",") != "temperature,top_p" {
				t.Errorf("%s %s drops %v", p, m, got)
			}
		}
	}
	for _, p := range []routing.ProviderID{"gemini", "mistral", "groq", "custom"} {
		if len(byID[p]) != 0 {
			t.Errorf("%s drops %v; its models take sampling fields", p, byID[p])
		}
	}
}

// A file written before drop_params existed, such as one the admin routes saved when a human
// enabled a provider, gets the defaults' lists for the models the defaults know. A list in the
// file wins, and the next save writes the lists out.
func TestAFileWithoutDropParamsGetsTheDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.toml")
	text := `
[[provider]]
id = "anthropic"
enabled = true
openai_base_url = "https://api.anthropic.com/v1"
key_required = true

  [[provider.model]]
  id = "claude-haiku-5-5"
  tier = "quick"
  family = "claude"
  input_usd_per_mtok = "0.10"
  output_usd_per_mtok = "0.50"

  [[provider.model]]
  id = "claude-sonnet-5-5"
  tier = "normal"
  family = "claude"
  input_usd_per_mtok = "2.00"
  output_usd_per_mtok = "10.00"
  drop_params = ["temperature"]

  [[provider.model]]
  id = "claude-next"
  tier = "deep"
  family = "claude"
  input_usd_per_mtok = "4.00"
  output_usd_per_mtok = "20.00"
`
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := gatewayconfig.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	drop := f.ProviderConfigs()[0].DropParams
	if strings.Join(drop["claude-haiku-5-5"], ",") != "temperature,top_p,top_k" {
		t.Errorf("haiku drops %v, want the defaults", drop["claude-haiku-5-5"])
	}
	if strings.Join(drop["claude-sonnet-5-5"], ",") != "temperature" {
		t.Errorf("sonnet drops %v, want the file's list", drop["claude-sonnet-5-5"])
	}
	if _, ok := drop["claude-next"]; ok {
		t.Errorf("a model the defaults do not know drops %v", drop["claude-next"])
	}
	if err := gatewayconfig.Save(path, f); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path) //nolint:gosec // G304: the path is inside t.TempDir.
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `drop_params = ['temperature', 'top_p', 'top_k']`) {
		t.Errorf("saved file misses the haiku list:\n%s", raw)
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
		"drops the model":  "[[provider]]\nid = \"x\"\nopenai_base_url = \"http://x\"\n[[provider.model]]\nid = \"m\"\ntier = \"quick\"\ninput_usd_per_mtok = \"0\"\noutput_usd_per_mtok = \"0\"\ndrop_params = [\"model\"]\n",
		"drops the limit":  "[[provider]]\nid = \"x\"\nopenai_base_url = \"http://x\"\n[[provider.model]]\nid = \"m\"\ntier = \"quick\"\ninput_usd_per_mtok = \"0\"\noutput_usd_per_mtok = \"0\"\ndrop_params = [\"max_tokens\"]\n",
		"drops nothing":    "[[provider]]\nid = \"x\"\nopenai_base_url = \"http://x\"\n[[provider.model]]\nid = \"m\"\ntier = \"quick\"\ninput_usd_per_mtok = \"0\"\noutput_usd_per_mtok = \"0\"\ndrop_params = [\" \"]\n",
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

func TestServerSectionIsGone(t *testing.T) {
	// The edge serve command sets where the gateway and the admin socket listen; a [server]
	// section from an older file is refused rather than silently ignored.
	path := filepath.Join(t.TempDir(), "gateway.toml")
	if err := os.WriteFile(path, []byte("[server]\nlisten = \"10.88.0.2:4000\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := gatewayconfig.Load(path); err == nil {
		t.Fatal("Load accepted a [server] section")
	}
}

func TestStore_ListsTheDefaultsWithoutAFile(t *testing.T) {
	s := gatewayconfig.NewStore(filepath.Join(t.TempDir(), "gateway.toml"))

	list, err := s.List()

	if err != nil || len(list) != len(gatewayconfig.Default().Providers) || list[0].Enabled {
		t.Fatalf("List() = %d providers, %v; want the disabled defaults", len(list), err)
	}
}

func TestStore_SetEnabledWritesTheFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.toml")
	s := gatewayconfig.NewStore(path)

	if err := s.SetEnabled("openai", true); err != nil {
		t.Fatalf("SetEnabled() error = %v", err)
	}

	f, err := gatewayconfig.Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	for _, p := range f.Providers {
		if (p.ID == "openai") != p.Enabled {
			t.Errorf("provider %s enabled = %v", p.ID, p.Enabled)
		}
	}
	if err := s.SetEnabled("nope", true); !errors.Is(err, gatewayconfig.ErrUnknownProvider) {
		t.Errorf("SetEnabled(nope) error = %v", err)
	}
}

func TestStore_ReportsABrokenFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.toml")
	if err := os.WriteFile(path, []byte("[limits\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := gatewayconfig.NewStore(path)

	if _, err := s.List(); err == nil {
		t.Error("List() on a broken file succeeded")
	}
	if err := s.SetEnabled("openai", true); err == nil {
		t.Error("SetEnabled() on a broken file succeeded")
	}
}

func TestSaveFailsInMissingDirectory(t *testing.T) {
	if err := gatewayconfig.Save(filepath.Join(t.TempDir(), "no", "such", "gateway.toml"), gatewayconfig.Default()); err == nil {
		t.Fatal("Save succeeded in a missing directory")
	}
}
