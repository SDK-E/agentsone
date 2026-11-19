// SPDX-License-Identifier: MIT

// Package domain is the gateway's domain: the model catalog, the routing
// policy that turns the one public model name into a real model, call cost,
// and the per-provider circuit breaker. It does no input or output.
//
// Routing rules:
//
//   - Agents call one name, autoagents/one, with a weight hint: quick, normal
//     or deep (default normal). The name suffix (autoagents/one:deep) wins over
//     the X-Agentsone-Weight header.
//   - Only enabled, healthy providers are used.
//   - Engine constraint: the Anthropic Messages endpoint is routed only to
//     Claude models on providers that speak the Messages API. The
//     OpenAI-compatible endpoint may be routed to any provider that speaks the
//     OpenAI chat completions API.
//   - Inside the requested tier the cheapest model wins (input plus output
//     price per million tokens; ties broken by provider then model id).
//   - When the tier has no model, neighbour tiers are tried: quick goes to
//     normal then deep, normal to deep then quick, deep to normal then quick.
package domain

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	ledger "github.com/SDK-E/agentsone/internal/edge/budget/domain"
)

// PublicModel is the only model name agents may ask for.
const PublicModel = "autoagents/one"

// Errors returned by this package. Callers match them with errors.Is.
var (
	ErrUnknownModel = errors.New("routing: unknown model name")
	ErrBadWeight    = errors.New("routing: unknown weight")
	ErrBadCatalog   = errors.New("routing: invalid catalog")
)

// Weight is the hint that picks a tier.
type Weight string

// The three weights, which are also the three model tiers.
const (
	Quick  Weight = "quick"
	Normal Weight = "normal"
	Deep   Weight = "deep"
)

// ParseWeight reads a weight name, ignoring case and surrounding space.
func ParseWeight(s string) (Weight, error) {
	switch w := Weight(strings.ToLower(strings.TrimSpace(s))); w {
	case Quick, Normal, Deep:
		return w, nil
	default:
		return "", fmt.Errorf("%w: %q (use quick, normal or deep)", ErrBadWeight, s)
	}
}

// ResolveWeight checks the requested model name and returns the weight from
// its suffix, else from the header value, else Normal.
func ResolveWeight(model, header string) (Weight, error) {
	if model == PublicModel {
		if strings.TrimSpace(header) == "" {
			return Normal, nil
		}
		return ParseWeight(header)
	}
	suffix, ok := strings.CutPrefix(model, PublicModel+":")
	if !ok {
		return "", fmt.Errorf("%w: %q (the only model is %s)", ErrUnknownModel, model, PublicModel)
	}
	return ParseWeight(suffix)
}

// fallback lists the tiers to try for each weight, nearest first.
var fallback = map[Weight][]Weight{
	Quick:  {Quick, Normal, Deep},
	Normal: {Normal, Deep, Quick},
	Deep:   {Deep, Normal, Quick},
}

// API is a wire protocol a provider speaks. It is also the engine constraint
// of a gateway endpoint.
type API string

// The two APIs.
const (
	APIOpenAI    API = "openai"
	APIAnthropic API = "anthropic"
)

// ProviderID names a provider, such as "anthropic" or "groq".
type ProviderID string

// Provider is a model vendor or endpoint the human may enable.
type Provider struct {
	ID      ProviderID
	APIs    []API
	Enabled bool
}

// Supports reports whether the provider speaks api.
func (p Provider) Supports(api API) bool { return slices.Contains(p.APIs, api) }

// ClaudeFamily is the Family value of Claude models.
const ClaudeFamily = "claude"

// Model is one real model with its tier and price.
type Model struct {
	Provider ProviderID
	ID       string
	Tier     Weight
	// Family groups models by maker, such as "claude"; the Anthropic engine
	// may only use the claude family.
	Family string
	// Prices are micro-dollars per million tokens.
	InputPerMTok  ledger.Micros
	OutputPerMTok ledger.Micros
}

// IsClaude reports whether this is a Claude model.
func (m Model) IsClaude() bool { return m.Family == ClaudeFamily }

func (m Model) price() ledger.Micros { return m.InputPerMTok + m.OutputPerMTok }

// Usage is the token count of one call.
type Usage struct {
	Input      int64
	Output     int64
	CacheWrite int64
	CacheRead  int64
}

// Cost is the price of usage on this model, rounded up to a whole
// micro-dollar. Cache writes cost 1.25 times the input price and cache reads
// 0.1 times.
func (m Model) Cost(u Usage) ledger.Micros {
	in, out := int64(m.InputPerMTok), int64(m.OutputPerMTok)
	// Hundredths of a micro-dollar per million tokens, so the cache factors
	// stay whole numbers.
	n := 100*u.Input*in + 100*u.Output*out + 125*u.CacheWrite*in + 10*u.CacheRead*in
	const div = 100 * 1_000_000
	return ledger.Micros((n + div - 1) / div)
}

// BytesPerToken is the estimate of request bytes per input token. Real text
// runs near four bytes per token; three keeps the estimate above the actual.
const BytesPerToken = 3

// Estimate is the reservation for a call: the request bytes as input tokens
// plus the full output allowance, and never less than one micro-dollar, so
// that at zero budget every call is refused, free models included.
func Estimate(m Model, requestBytes, maxOutputTokens int64) ledger.Micros {
	in := (requestBytes + BytesPerToken - 1) / BytesPerToken
	return max(m.Cost(Usage{Input: in, Output: maxOutputTokens}), 1)
}

// Catalog is every known provider and model.
type Catalog struct {
	Providers []Provider
	Models    []Model
}

// Provider finds a provider by id.
func (c Catalog) Provider(id ProviderID) (Provider, bool) {
	for _, p := range c.Providers {
		if p.ID == id {
			return p, true
		}
	}
	return Provider{}, false
}

// Validate checks that ids are present and unique, APIs and tiers are known,
// models name a known provider, and prices are not negative.
func (c Catalog) Validate() error {
	seen := map[ProviderID]bool{}
	for _, p := range c.Providers {
		if p.ID == "" {
			return fmt.Errorf("%w: provider without id", ErrBadCatalog)
		}
		if seen[p.ID] {
			return fmt.Errorf("%w: provider %q listed twice", ErrBadCatalog, p.ID)
		}
		seen[p.ID] = true
		if len(p.APIs) == 0 {
			return fmt.Errorf("%w: provider %q has no API", ErrBadCatalog, p.ID)
		}
		for _, a := range p.APIs {
			if a != APIOpenAI && a != APIAnthropic {
				return fmt.Errorf("%w: provider %q has unknown API %q", ErrBadCatalog, p.ID, a)
			}
		}
	}
	models := map[string]bool{}
	for _, m := range c.Models {
		if !seen[m.Provider] {
			return fmt.Errorf("%w: model %q names unknown provider %q", ErrBadCatalog, m.ID, m.Provider)
		}
		if m.ID == "" {
			return fmt.Errorf("%w: model without id under %q", ErrBadCatalog, m.Provider)
		}
		if _, err := ParseWeight(string(m.Tier)); err != nil || string(m.Tier) != strings.ToLower(string(m.Tier)) {
			return fmt.Errorf("%w: model %q has unknown tier %q", ErrBadCatalog, m.ID, m.Tier)
		}
		if m.InputPerMTok < 0 || m.OutputPerMTok < 0 {
			return fmt.Errorf("%w: model %q has a negative price", ErrBadCatalog, m.ID)
		}
		key := string(m.Provider) + "\x00" + m.ID
		if models[key] {
			return fmt.Errorf("%w: model %q listed twice under %q", ErrBadCatalog, m.ID, m.Provider)
		}
		models[key] = true
	}
	return nil
}

// Request is what a caller asks the router for.
type Request struct {
	// Engine is the API of the endpoint the call came in on.
	Engine API
	Weight Weight
}

// Rank returns every model that may serve the request, best first. healthy
// reports whether a provider's breaker lets calls through.
func Rank(c Catalog, r Request, healthy func(ProviderID) bool) []Model {
	tiers, ok := fallback[r.Weight]
	if !ok {
		tiers = fallback[Normal]
	}
	var out []Model
	for _, tier := range tiers {
		var inTier []Model
		for _, m := range c.Models {
			if m.Tier != tier || !eligible(c, r.Engine, m, healthy) {
				continue
			}
			inTier = append(inTier, m)
		}
		slices.SortFunc(inTier, func(a, b Model) int {
			if d := a.price() - b.price(); d != 0 {
				if d < 0 {
					return -1
				}
				return 1
			}
			if c := strings.Compare(string(a.Provider), string(b.Provider)); c != 0 {
				return c
			}
			return strings.Compare(a.ID, b.ID)
		})
		out = append(out, inTier...)
	}
	return out
}

func eligible(c Catalog, engine API, m Model, healthy func(ProviderID) bool) bool {
	p, ok := c.Provider(m.Provider)
	if !ok || !p.Enabled || !p.Supports(engine) || !healthy(p.ID) {
		return false
	}
	if engine == APIAnthropic && !m.IsClaude() {
		return false
	}
	return true
}
