// SPDX-License-Identifier: MIT

// Package config reads and writes gateway.toml, the human-editable
// list of providers, their models with tier and price, and the gateway's
// limits. Without a file the defaults apply: every provider known, every
// provider disabled.
//
// Prices are text in US dollars per million tokens, such as "0.25", so they
// convert to micro-dollars exactly. The default prices are starting points
// taken from the providers' price lists; check them against the providers'
// own pages and edit the file when they change.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"

	"github.com/pelletier/go-toml/v2"

	ledger "github.com/SDK-E/agentsone/internal/edge/budget/domain"
	"github.com/SDK-E/agentsone/internal/edge/gateway/adapters/providers"
	routing "github.com/SDK-E/agentsone/internal/edge/gateway/domain"
)

// FileName is the config file's name inside the host data folder.
const FileName = "gateway.toml"

// ErrUnknownProvider is returned for a provider id not in the file.
var ErrUnknownProvider = errors.New("gatewayconfig: unknown provider")

// Limits are the gateway's tunables.
type Limits struct {
	DefaultMaxOutputTokens int64 `toml:"default_max_output_tokens" comment:"Output limit for calls that set none."`
	MaxOutputTokensCap     int64 `toml:"max_output_tokens_cap" comment:"Largest output limit a call may have."`
	MaxAttempts            int   `toml:"max_attempts" comment:"Providers one call may try."`
	RetryAfterSeconds      int   `toml:"retry_after_seconds" comment:"Sent with budget and availability refusals."`
	FlushIntervalSeconds   int   `toml:"flush_interval_seconds" comment:"How often spending is written to budget.db."`
	BreakerFailures        int   `toml:"breaker_failures" comment:"Failures in a row that take a provider out."`
	BreakerCooldownSeconds int   `toml:"breaker_cooldown_seconds" comment:"How long a failing provider stays out."`
	HeaderTimeoutSeconds   int   `toml:"header_timeout_seconds" comment:"Wait for a provider's first answer."`
}

// Model is one model of a provider.
type Model struct {
	ID     string `toml:"id"`
	Tier   string `toml:"tier" comment:"quick, normal or deep"`
	Family string `toml:"family" comment:"claude for Claude models"`
	Input  string `toml:"input_usd_per_mtok"`
	Output string `toml:"output_usd_per_mtok"`
}

// Provider is one provider.
type Provider struct {
	ID               string  `toml:"id"`
	Enabled          bool    `toml:"enabled"`
	OpenAIBaseURL    string  `toml:"openai_base_url,omitempty"`
	AnthropicBaseURL string  `toml:"anthropic_base_url,omitempty"`
	MaxTokensField   string  `toml:"max_tokens_field,omitempty"`
	KeyRequired      bool    `toml:"key_required"`
	Models           []Model `toml:"model"`
}

// AdminSocketName is the admin socket's default name inside the data folder.
const AdminSocketName = "admin.sock"

// Server says where the gateway and its admin socket listen.
type Server struct {
	Listen      string `toml:"listen,omitempty" comment:"Gateway address on the machine-side inner network, host:port."`
	AdminSocket string `toml:"admin_socket,omitempty" comment:"Absolute path of the admin socket; empty means admin.sock in the data folder."`
}

// AdminSocketPath gives the admin socket path, defaulting into dataDir.
func (s Server) AdminSocketPath(dataDir string) string {
	if s.AdminSocket != "" {
		return s.AdminSocket
	}
	return filepath.Join(dataDir, AdminSocketName)
}

func (s Server) validate() error {
	_, port, err := net.SplitHostPort(s.Listen)
	if err != nil {
		return fmt.Errorf("server: listen %q must be host:port: %w", s.Listen, err)
	}
	if n, err := strconv.Atoi(port); err != nil || n < 0 || n > 65535 {
		return fmt.Errorf("server: listen %q has a bad port", s.Listen)
	}
	if s.AdminSocket != "" && !filepath.IsAbs(s.AdminSocket) {
		return fmt.Errorf("server: admin_socket %q must be an absolute path", s.AdminSocket)
	}
	return nil
}

// File is the whole config.
type File struct {
	Server    Server     `toml:"server"`
	Limits    Limits     `toml:"limits"`
	Providers []Provider `toml:"provider"`
}

// Default returns the built-in config.
func Default() File {
	return File{
		Server: Server{Listen: ":4000"},
		Limits: Limits{
			DefaultMaxOutputTokens: 4096,
			MaxOutputTokensCap:     32768,
			MaxAttempts:            3,
			RetryAfterSeconds:      30,
			FlushIntervalSeconds:   5,
			BreakerFailures:        3,
			BreakerCooldownSeconds: 30,
			HeaderTimeoutSeconds:   60,
		},
		Providers: []Provider{
			{
				ID: "anthropic", OpenAIBaseURL: "https://api.anthropic.com/v1",
				AnthropicBaseURL: "https://api.anthropic.com/v1", KeyRequired: true,
				Models: []Model{
					{ID: "claude-haiku-5-5", Tier: "quick", Family: "claude", Input: "0.10", Output: "0.50"},
					{ID: "claude-sonnet-5-5", Tier: "normal", Family: "claude", Input: "2.00", Output: "10.00"},
					{ID: "claude-opus-5-5", Tier: "deep", Family: "claude", Input: "4.00", Output: "20.00"},
				},
			},
			{
				ID: "openai", OpenAIBaseURL: "https://api.openai.com/v1", MaxTokensField: "max_completion_tokens", KeyRequired: true,
				Models: []Model{
					{ID: "gpt-5-nano", Tier: "quick", Family: "gpt", Input: "0.05", Output: "0.40"},
					{ID: "gpt-5-mini", Tier: "normal", Family: "gpt", Input: "0.25", Output: "2.00"},
					{ID: "gpt-5", Tier: "deep", Family: "gpt", Input: "1.25", Output: "10.00"},
				},
			},
			{
				ID: "openrouter", OpenAIBaseURL: "https://openrouter.ai/api/v1", KeyRequired: true,
				Models: []Model{
					{ID: "openai/gpt-5-nano", Tier: "quick", Family: "gpt", Input: "0.05", Output: "0.40"},
					{ID: "openai/gpt-5-mini", Tier: "normal", Family: "gpt", Input: "0.25", Output: "2.00"},
					{ID: "openai/gpt-5", Tier: "deep", Family: "gpt", Input: "1.25", Output: "10.00"},
				},
			},
			{
				ID: "gemini", OpenAIBaseURL: "https://generativelanguage.googleapis.com/v1beta/openai", KeyRequired: true,
				Models: []Model{
					{ID: "gemini-2.5-flash-lite", Tier: "quick", Family: "gemini", Input: "0.10", Output: "0.40"},
					{ID: "gemini-2.5-flash", Tier: "normal", Family: "gemini", Input: "0.30", Output: "2.50"},
					{ID: "gemini-2.5-pro", Tier: "deep", Family: "gemini", Input: "1.25", Output: "10.00"},
				},
			},
			{
				ID: "mistral", OpenAIBaseURL: "https://api.mistral.ai/v1", KeyRequired: true,
				Models: []Model{
					{ID: "mistral-small-latest", Tier: "quick", Family: "mistral", Input: "0.10", Output: "0.30"},
					{ID: "mistral-medium-latest", Tier: "normal", Family: "mistral", Input: "0.40", Output: "2.00"},
					{ID: "mistral-large-latest", Tier: "deep", Family: "mistral", Input: "2.00", Output: "6.00"},
				},
			},
			{
				ID: "groq", OpenAIBaseURL: "https://api.groq.com/openai/v1", KeyRequired: true,
				Models: []Model{
					{ID: "llama-3.1-8b-instant", Tier: "quick", Family: "llama", Input: "0.05", Output: "0.08"},
					{ID: "llama-3.3-70b-versatile", Tier: "normal", Family: "llama", Input: "0.59", Output: "0.79"},
				},
			},
			{
				// Any OpenAI-compatible server, such as Ollama on the machine
				// running the compose stack.
				ID: "custom", OpenAIBaseURL: "http://host.docker.internal:11434/v1", KeyRequired: false,
				Models: []Model{
					{ID: "llama3.2", Tier: "normal", Family: "llama", Input: "0", Output: "0"},
				},
			},
		},
	}
}

// Load reads the file at path. A missing file gives Default. Limits the file
// leaves out keep their defaults.
func Load(path string) (File, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // G304: the path is the host data folder, chosen by the host tool.
	if errors.Is(err, fs.ErrNotExist) {
		return Default(), nil
	}
	if err != nil {
		return File{}, fmt.Errorf("gatewayconfig: read %s: %w", path, err)
	}
	def := Default()
	f := File{Server: def.Server, Limits: def.Limits}
	dec := toml.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		return File{}, fmt.Errorf("gatewayconfig: %s: %w", path, err)
	}
	if err := f.Validate(); err != nil {
		return File{}, fmt.Errorf("gatewayconfig: %s: %w", path, err)
	}
	return f, nil
}

// Save writes the file atomically, readable only by the user.
func Save(path string, f File) error {
	body, err := toml.Marshal(f)
	if err != nil {
		return fmt.Errorf("gatewayconfig: encode: %w", err)
	}
	header := "# Agents One model gateway. Edit freely; `agentsone gateway providers enable <id>` also\n" +
		"# writes here. Prices are US dollars per million tokens. Keys are not kept here: they are\n" +
		"# files in the edge volume, written only through the admin socket\n" +
		"# (`agentsone gateway key set <provider>`).\n\n"
	tmp, err := os.CreateTemp(filepath.Dir(path), ".gateway-*.toml")
	if err != nil {
		return fmt.Errorf("gatewayconfig: save %s: %w", path, err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.WriteString(header + string(body)); err != nil {
		return errors.Join(fmt.Errorf("gatewayconfig: write %s: %w", path, err), tmp.Close())
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("gatewayconfig: write %s: %w", path, err)
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		return fmt.Errorf("gatewayconfig: chmod %s: %w", path, err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("gatewayconfig: save %s: %w", path, err)
	}
	return nil
}

// SetEnabled switches a provider on or off.
func (f *File) SetEnabled(id string, on bool) error {
	for i := range f.Providers {
		if f.Providers[i].ID == id {
			f.Providers[i].Enabled = on
			return nil
		}
	}
	return fmt.Errorf("%w: %q", ErrUnknownProvider, id)
}

// Validate checks URLs, output limit fields and the catalog.
func (f File) Validate() error {
	if err := f.Server.validate(); err != nil {
		return err
	}
	for _, p := range f.Providers {
		for _, u := range []string{p.OpenAIBaseURL, p.AnthropicBaseURL} {
			if u == "" {
				continue
			}
			parsed, err := url.Parse(u)
			if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" {
				return fmt.Errorf("provider %q: base URL %q must be http or https", p.ID, u)
			}
		}
		switch p.MaxTokensField {
		case "", "max_tokens", "max_completion_tokens":
		default:
			return fmt.Errorf("provider %q: max_tokens_field must be max_tokens or max_completion_tokens", p.ID)
		}
	}
	_, err := f.Catalog()
	return err
}

// Catalog converts the file to the routing catalog and validates it.
func (f File) Catalog() (routing.Catalog, error) {
	var c routing.Catalog
	for _, p := range f.Providers {
		rp := routing.Provider{ID: routing.ProviderID(p.ID), Enabled: p.Enabled}
		if p.AnthropicBaseURL != "" {
			rp.APIs = append(rp.APIs, routing.APIAnthropic)
		}
		if p.OpenAIBaseURL != "" {
			rp.APIs = append(rp.APIs, routing.APIOpenAI)
		}
		c.Providers = append(c.Providers, rp)
		for _, m := range p.Models {
			in, err := ledger.ParseUSD(m.Input)
			if err != nil {
				return routing.Catalog{}, fmt.Errorf("model %q input price: %w", m.ID, err)
			}
			out, err := ledger.ParseUSD(m.Output)
			if err != nil {
				return routing.Catalog{}, fmt.Errorf("model %q output price: %w", m.ID, err)
			}
			c.Models = append(c.Models, routing.Model{
				Provider: rp.ID, ID: m.ID, Tier: routing.Weight(m.Tier), Family: m.Family,
				InputPerMTok: in, OutputPerMTok: out,
			})
		}
	}
	if err := c.Validate(); err != nil {
		return routing.Catalog{}, err
	}
	return c, nil
}

// ProviderConfigs gives the provider client its settings.
func (f File) ProviderConfigs() []providers.Config {
	out := make([]providers.Config, 0, len(f.Providers))
	for _, p := range f.Providers {
		out = append(out, providers.Config{
			ID: routing.ProviderID(p.ID), OpenAIBaseURL: p.OpenAIBaseURL, AnthropicBaseURL: p.AnthropicBaseURL,
			MaxTokensField: p.MaxTokensField, KeyRequired: p.KeyRequired,
		})
	}
	return out
}
