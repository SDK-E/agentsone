// SPDX-License-Identifier: MIT

package routing_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/host/budget/ledger"
	"github.com/SDK-E/agentsone/internal/host/gateway/routing"
)

func usd(s string) ledger.Micros {
	m, err := ledger.ParseUSD(s)
	if err != nil {
		panic(err)
	}
	return m
}

func model(p routing.ProviderID, id string, tier routing.Weight, family, in, out string) routing.Model {
	return routing.Model{Provider: p, ID: id, Tier: tier, Family: family, InputPerMTok: usd(in), OutputPerMTok: usd(out)}
}

var catalog = routing.Catalog{
	Providers: []routing.Provider{
		{ID: "anthropic", APIs: []routing.API{routing.APIAnthropic, routing.APIOpenAI}, Enabled: true},
		{ID: "openai", APIs: []routing.API{routing.APIOpenAI}, Enabled: true},
		{ID: "groq", APIs: []routing.API{routing.APIOpenAI}, Enabled: false},
	},
	Models: []routing.Model{
		model("anthropic", "claude-haiku-5-5", routing.Quick, "claude", "0.10", "0.50"),
		model("anthropic", "claude-sonnet-5-5", routing.Normal, "claude", "2", "10"),
		model("anthropic", "claude-opus-5-5", routing.Deep, "claude", "4", "20"),
		model("openai", "gpt-small", routing.Quick, "gpt", "0.05", "0.40"),
		model("openai", "gpt-mid", routing.Normal, "gpt", "1", "8"),
		model("groq", "llama-cheap", routing.Normal, "llama", "0.01", "0.01"),
	},
}

func allHealthy(routing.ProviderID) bool { return true }

func TestRankPicksCheapestEligibleModel(t *testing.T) {
	tests := []struct {
		name    string
		engine  routing.API
		weight  routing.Weight
		healthy func(routing.ProviderID) bool
		cat     routing.Catalog
		want    []string
	}{
		{
			name:   "openai engine, normal: cheapest normal first, then neighbours",
			engine: routing.APIOpenAI, weight: routing.Normal, healthy: allHealthy, cat: catalog,
			want: []string{"gpt-mid", "claude-sonnet-5-5", "claude-opus-5-5", "gpt-small", "claude-haiku-5-5"},
		},
		{
			name:   "anthropic engine only gets Claude",
			engine: routing.APIAnthropic, weight: routing.Quick, healthy: allHealthy, cat: catalog,
			want: []string{"claude-haiku-5-5", "claude-sonnet-5-5", "claude-opus-5-5"},
		},
		{
			name:   "unhealthy provider skipped",
			engine: routing.APIOpenAI, weight: routing.Quick,
			healthy: func(p routing.ProviderID) bool { return p != "openai" }, cat: catalog,
			want: []string{"claude-haiku-5-5", "claude-sonnet-5-5", "claude-opus-5-5"},
		},
		{
			name:   "deep falls back to normal then quick",
			engine: routing.APIOpenAI, weight: routing.Deep, healthy: allHealthy,
			cat: routing.Catalog{Providers: catalog.Providers, Models: []routing.Model{
				model("openai", "gpt-small", routing.Quick, "gpt", "0.05", "0.40"),
				model("openai", "gpt-mid", routing.Normal, "gpt", "1", "8"),
			}},
			want: []string{"gpt-mid", "gpt-small"},
		},
		{
			name:   "quick falls back upwards",
			engine: routing.APIOpenAI, weight: routing.Quick, healthy: allHealthy,
			cat: routing.Catalog{Providers: catalog.Providers, Models: []routing.Model{
				model("openai", "gpt-mid", routing.Normal, "gpt", "1", "8"),
			}},
			want: []string{"gpt-mid"},
		},
		{
			name:   "Claude on a provider without the Messages API is not used for the anthropic engine",
			engine: routing.APIAnthropic, weight: routing.Normal, healthy: allHealthy,
			cat: routing.Catalog{
				Providers: []routing.Provider{{ID: "openrouter", APIs: []routing.API{routing.APIOpenAI}, Enabled: true}},
				Models:    []routing.Model{model("openrouter", "anthropic/claude-x", routing.Normal, "claude", "1", "1")},
			},
			want: nil,
		},
		{
			name:   "non-Claude model on an Anthropic-API provider is not used for the anthropic engine",
			engine: routing.APIAnthropic, weight: routing.Normal, healthy: allHealthy,
			cat: routing.Catalog{
				Providers: []routing.Provider{{ID: "proxy", APIs: []routing.API{routing.APIAnthropic}, Enabled: true}},
				Models:    []routing.Model{model("proxy", "gpt-mid", routing.Normal, "gpt", "1", "1")},
			},
			want: nil,
		},
		{
			name:   "price ties broken by provider then model id",
			engine: routing.APIOpenAI, weight: routing.Normal, healthy: allHealthy,
			cat: routing.Catalog{
				Providers: []routing.Provider{
					{ID: "b", APIs: []routing.API{routing.APIOpenAI}, Enabled: true},
					{ID: "a", APIs: []routing.API{routing.APIOpenAI}, Enabled: true},
				},
				Models: []routing.Model{
					model("b", "m1", routing.Normal, "x", "1", "1"),
					model("a", "m2", routing.Normal, "x", "1", "1"),
					model("a", "m1", routing.Normal, "x", "1", "1"),
				},
			},
			want: []string{"a/m1", "a/m2", "b/m1"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := routing.Rank(tc.cat, routing.Request{Engine: tc.engine, Weight: tc.weight}, tc.healthy)
			if len(got) != len(tc.want) {
				t.Fatalf("Rank = %v, want %v", ids(got, tc.want), tc.want)
			}
			for i := range got {
				if name := id(got[i], tc.want[i]); name != tc.want[i] {
					t.Fatalf("Rank[%d] = %s, want %s (all %v)", i, name, tc.want[i], ids(got, tc.want))
				}
			}
		})
	}
}

// id names a model as the want list does: "provider/model" when the wanted
// name has a slash, else the bare model id.
func id(m routing.Model, like string) string {
	if strings.Contains(like, "/") {
		return string(m.Provider) + "/" + m.ID
	}
	return m.ID
}

func ids(ms []routing.Model, like []string) []string {
	out := make([]string, len(ms))
	for i, m := range ms {
		l := ""
		if len(like) > 0 {
			l = like[0]
		}
		out[i] = id(m, l)
	}
	return out
}

func TestParseWeightAndModelName(t *testing.T) {
	tests := []struct {
		model, header string
		want          routing.Weight
		wantErr       bool
	}{
		{"autoagents/one", "", routing.Normal, false},
		{"autoagents/one", "quick", routing.Quick, false},
		{"autoagents/one", "DEEP", routing.Deep, false},
		{"autoagents/one:deep", "", routing.Deep, false},
		{"autoagents/one:quick", "deep", routing.Quick, false}, // the name wins over the header
		{"autoagents/one", "heavy", "", true},
		{"autoagents/one:heavy", "", "", true},
		{"gpt-4o", "", "", true},
		{"", "", "", true},
	}
	for _, tc := range tests {
		got, err := routing.ResolveWeight(tc.model, tc.header)
		if (err != nil) != tc.wantErr || got != tc.want {
			t.Fatalf("ResolveWeight(%q, %q) = %q, %v; want %q, err=%v", tc.model, tc.header, got, err, tc.want, tc.wantErr)
		}
		if tc.wantErr && !errors.Is(err, routing.ErrUnknownModel) && !errors.Is(err, routing.ErrBadWeight) {
			t.Fatalf("ResolveWeight error %v is neither ErrUnknownModel nor ErrBadWeight", err)
		}
	}
}

func TestCost(t *testing.T) {
	m := model("anthropic", "claude-opus-5-5", routing.Deep, "claude", "4", "20")
	tests := []struct {
		name string
		u    routing.Usage
		want ledger.Micros
	}{
		{"zero", routing.Usage{}, 0},
		{"input only", routing.Usage{Input: 1_000_000}, usd("4")},
		{"output only", routing.Usage{Output: 1_000}, usd("0.02")},
		{"rounds up to a whole micro-dollar", routing.Usage{Input: 1}, 4},
		{"tiny rounds up", routing.Usage{Output: 1}, 20},
		{"cache write costs 1.25x input", routing.Usage{CacheWrite: 1_000_000}, usd("5")},
		{"cache read costs 0.1x input", routing.Usage{CacheRead: 1_000_000}, usd("0.4")},
	}
	for _, tc := range tests {
		if got := m.Cost(tc.u); got != tc.want {
			t.Fatalf("%s: Cost = %v, want %v", tc.name, got, tc.want)
		}
	}
	free := model("ollama", "llama", routing.Normal, "llama", "0", "0")
	if got := free.Cost(routing.Usage{Input: 5000, Output: 5000}); got != 0 {
		t.Fatalf("free model cost = %v", got)
	}
}

func TestEstimateIsAnUpperBoundAndNeverZero(t *testing.T) {
	m := model("anthropic", "claude-sonnet-5-5", routing.Normal, "claude", "2", "10")
	got := routing.Estimate(m, 3000, 1000) // 3000 request bytes, 1000 max output tokens
	// 3000 bytes / 3 = 1000 input tokens -> $0.002; 1000 output tokens -> $0.01.
	if got != usd("0.012") {
		t.Fatalf("Estimate = %v, want $0.012", got)
	}
	free := model("ollama", "llama", routing.Normal, "llama", "0", "0")
	if got := routing.Estimate(free, 10, 10); got != 1 {
		t.Fatalf("free Estimate = %v, want 1 micro-dollar", got)
	}
}

func TestBreakerTripsAndRecovers(t *testing.T) {
	t0 := time.Date(2026, 11, 19, 20, 0, 0, 0, time.UTC)
	b := routing.NewBreaker(3, 30*time.Second)
	const p = routing.ProviderID("openai")

	if !b.Healthy(p, t0) {
		t.Fatal("new provider not healthy")
	}
	b.Failure(p, t0)
	b.Failure(p, t0)
	b.Success(p) // a success resets the count
	b.Failure(p, t0)
	b.Failure(p, t0)
	if !b.Healthy(p, t0) {
		t.Fatal("tripped before threshold")
	}
	b.Failure(p, t0)
	if b.Healthy(p, t0) || b.State(p, t0) != routing.BreakerOpen {
		t.Fatal("breaker did not open at threshold")
	}
	if b.Healthy(p, t0.Add(29*time.Second)) {
		t.Fatal("breaker closed before cooldown")
	}

	// After the cooldown one probe is let through.
	t1 := t0.Add(30 * time.Second)
	if b.State(p, t1) != routing.BreakerHalfOpen {
		t.Fatalf("state after cooldown = %v, want half-open", b.State(p, t1))
	}
	if !b.Allow(p, t1) {
		t.Fatal("probe not allowed after cooldown")
	}
	if b.Allow(p, t1) {
		t.Fatal("second probe allowed while first is in flight")
	}
	// The probe fails: open again for another cooldown.
	b.Failure(p, t1)
	if b.Healthy(p, t1.Add(10*time.Second)) {
		t.Fatal("breaker not reopened after failed probe")
	}
	t2 := t1.Add(30 * time.Second)
	if !b.Allow(p, t2) {
		t.Fatal("probe not allowed after second cooldown")
	}
	b.Success(p)
	if b.State(p, t2) != routing.BreakerClosed || !b.Allow(p, t2) || !b.Allow(p, t2) {
		t.Fatal("breaker not closed after successful probe")
	}
	if got := routing.BreakerOpen.String(); got != "open" {
		t.Fatalf("String = %q", got)
	}
}

func TestBreakerAbandonFreesTheProbeAndKeepsTheCount(t *testing.T) {
	t0 := time.Date(2026, 11, 19, 20, 0, 0, 0, time.UTC)
	b := routing.NewBreaker(2, time.Minute)
	const p = routing.ProviderID("x")
	b.Failure(p, t0)
	b.Failure(p, t0)
	t1 := t0.Add(time.Minute)
	if !b.Allow(p, t1) {
		t.Fatal("probe not allowed")
	}
	b.Abandon(p) // the probe never reached the provider
	if !b.Allow(p, t1) {
		t.Fatal("probe not allowed again after Abandon")
	}

	// In closed state Abandon keeps the failure count.
	c := routing.NewBreaker(2, time.Minute)
	c.Failure(p, t0)
	c.Abandon(p)
	c.Failure(p, t0)
	if c.Healthy(p, t0) {
		t.Fatal("Abandon reset the failure count")
	}
}

func TestCatalogValidate(t *testing.T) {
	if err := catalog.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	bad := []routing.Catalog{
		{Providers: []routing.Provider{{ID: ""}}},
		{Providers: []routing.Provider{{ID: "a", APIs: []routing.API{routing.APIOpenAI}}, {ID: "a", APIs: []routing.API{routing.APIOpenAI}}}},
		{Providers: []routing.Provider{{ID: "a"}}},
		{Providers: []routing.Provider{{ID: "a", APIs: []routing.API{"soap"}}}},
		{Providers: []routing.Provider{{ID: "a", APIs: []routing.API{routing.APIOpenAI}}}, Models: []routing.Model{{Provider: "b", ID: "m", Tier: routing.Quick}}},
		{Providers: []routing.Provider{{ID: "a", APIs: []routing.API{routing.APIOpenAI}}}, Models: []routing.Model{{Provider: "a", ID: "", Tier: routing.Quick}}},
		{Providers: []routing.Provider{{ID: "a", APIs: []routing.API{routing.APIOpenAI}}}, Models: []routing.Model{{Provider: "a", ID: "m", Tier: "huge"}}},
		{Providers: []routing.Provider{{ID: "a", APIs: []routing.API{routing.APIOpenAI}}}, Models: []routing.Model{{Provider: "a", ID: "m", Tier: routing.Quick, InputPerMTok: -1}}},
		{Providers: []routing.Provider{{ID: "a", APIs: []routing.API{routing.APIOpenAI}}}, Models: []routing.Model{
			{Provider: "a", ID: "m", Tier: routing.Quick}, {Provider: "a", ID: "m", Tier: routing.Deep}}},
	}
	for i, c := range bad {
		if err := c.Validate(); !errors.Is(err, routing.ErrBadCatalog) {
			t.Fatalf("bad catalog %d: Validate error = %v", i, err)
		}
	}
}

func TestProviderLookup(t *testing.T) {
	p, ok := catalog.Provider("openai")
	if !ok || p.ID != "openai" || !p.Supports(routing.APIOpenAI) || p.Supports(routing.APIAnthropic) {
		t.Fatalf("Provider(openai) = %+v, %v", p, ok)
	}
	if _, ok := catalog.Provider("nope"); ok {
		t.Fatal("found an unknown provider")
	}
}
