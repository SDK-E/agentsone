// SPDX-License-Identifier: MIT

package app_test

import (
	"errors"
	"testing"

	"github.com/SDK-E/agentsone/internal/edge/control/app"
	"github.com/SDK-E/agentsone/internal/edge/control/controltest"
	"github.com/SDK-E/agentsone/internal/edge/control/domain"
)

func newPowers(t *testing.T) (*app.Powers, *controltest.Edge) {
	t.Helper()
	clock := controltest.NewClock(t0)
	edge := controltest.NewEdge(clock.Now,
		app.Provider{ID: "openai", KeyRequired: true, Models: []app.Model{{ID: "gpt-x", Tier: "normal"}}})
	return app.NewPowers(app.PowersOptions{Network: edge, Models: edge, Money: edge, Freeze: edge}), edge
}

func TestOverview_ShowsEveryPower(t *testing.T) {
	t.Parallel()
	p, edge := newPowers(t)
	if _, err := edge.Allow(t.Context(), "example.org", "docs"); err != nil {
		t.Fatal(err)
	}

	o := p.Overview(t.Context())

	if o.AllowErr != nil || len(o.Allow) != 1 || o.Allow[0].Pattern != "example.org" {
		t.Fatalf("allow list = %+v, %v", o.Allow, o.AllowErr)
	}
	if o.ProvidersErr != nil || len(o.Providers) != 1 {
		t.Fatalf("providers = %+v, %v", o.Providers, o.ProvidersErr)
	}
	if o.BudgetErr != nil || o.Budget.Currency != "USD" {
		t.Fatalf("budget = %+v, %v", o.Budget, o.BudgetErr)
	}
	if o.FreezeErr != nil || o.Freeze.Frozen {
		t.Fatalf("freeze = %+v, %v", o.Freeze, o.FreezeErr)
	}
}

func TestOverview_ReportsEachPartTheEdgeCouldNotGive(t *testing.T) {
	t.Parallel()
	p, edge := newPowers(t)
	edge.Err = controltest.ErrDown

	o := p.Overview(t.Context())

	for name, err := range map[string]error{"allow": o.AllowErr, "providers": o.ProvidersErr, "budget": o.BudgetErr, "freeze": o.FreezeErr} {
		if !errors.Is(err, app.ErrEdgeUnavailable) {
			t.Errorf("%s error = %v, want %v", name, err, app.ErrEdgeUnavailable)
		}
	}
}

func TestAllow_AddsATrimmedEntry(t *testing.T) {
	t.Parallel()
	p, edge := newPowers(t)

	if err := p.Allow(t.Context(), "  example.org ", " docs "); err != nil {
		t.Fatalf("Allow() error = %v", err)
	}

	got, _ := edge.AllowList(t.Context())
	if len(got) != 1 || got[0] != (app.AllowEntry{Pattern: "example.org", Why: "docs"}) {
		t.Fatalf("allow list = %+v", got)
	}
}

func TestAllow_RefusesAnEmptyName(t *testing.T) {
	t.Parallel()
	p, _ := newPowers(t)

	if err := p.Allow(t.Context(), "  ", "docs"); !errors.Is(err, app.ErrEmptyName) {
		t.Fatalf("Allow() error = %v, want %v", err, app.ErrEmptyName)
	}
}

func TestAllow_PassesOnTheEdgeRefusal(t *testing.T) {
	t.Parallel()
	p, _ := newPowers(t)

	err := p.Allow(t.Context(), "not a name", "docs")

	var refusal *app.Refusal
	if !errors.As(err, &refusal) {
		t.Fatalf("Allow() error = %v, want the edge refusal", err)
	}
}

func TestDisallow_RemovesTheEntry(t *testing.T) {
	t.Parallel()
	p, edge := newPowers(t)
	_, _ = edge.Allow(t.Context(), "example.org", "docs")

	if err := p.Disallow(t.Context(), "example.org"); err != nil {
		t.Fatalf("Disallow() error = %v", err)
	}
	if got, _ := edge.AllowList(t.Context()); len(got) != 0 {
		t.Fatalf("allow list = %+v, want empty", got)
	}
}

func TestSetKey_StoresTheTrimmedKeyAndReturnsOnlyItsFingerprint(t *testing.T) {
	t.Parallel()
	p, edge := newPowers(t)

	got, err := p.SetKey(t.Context(), "openai", "  sk-test-123  ")
	if err != nil {
		t.Fatalf("SetKey() error = %v", err)
	}

	if edge.Key("openai") != "sk-test-123" {
		t.Fatalf("edge key = %q, want the trimmed key", edge.Key("openai"))
	}
	if !got.Set || got.Fingerprint != controltest.Fingerprint("sk-test-123") {
		t.Fatalf("key state = %+v", got)
	}
}

func TestSetKey_RefusesAnEmptyKey(t *testing.T) {
	t.Parallel()
	p, _ := newPowers(t)

	if _, err := p.SetKey(t.Context(), "openai", " \n "); !errors.Is(err, app.ErrEmptyKey) {
		t.Fatalf("SetKey() error = %v, want %v", err, app.ErrEmptyKey)
	}
}

func TestClearKey_RemovesTheKey(t *testing.T) {
	t.Parallel()
	p, edge := newPowers(t)
	_, _ = edge.SetKey(t.Context(), "openai", "sk-test-123")

	if err := p.ClearKey(t.Context(), "openai"); err != nil {
		t.Fatalf("ClearKey() error = %v", err)
	}
	if edge.Key("openai") != "" {
		t.Fatal("the key is still there")
	}
}

func TestEnableAndDisable_SwitchTheProvider(t *testing.T) {
	t.Parallel()
	p, edge := newPowers(t)

	if err := p.Enable(t.Context(), "openai"); err != nil {
		t.Fatalf("Enable() error = %v", err)
	}
	if got, _ := edge.Providers(t.Context()); !got[0].Enabled {
		t.Fatal("provider is not enabled")
	}
	if err := p.Disable(t.Context(), "openai"); err != nil {
		t.Fatalf("Disable() error = %v", err)
	}
	if got, _ := edge.Providers(t.Context()); got[0].Enabled {
		t.Fatal("provider is still enabled")
	}
}

func TestSetBudget_ReadsDollarsAndPeriod(t *testing.T) {
	t.Parallel()
	p, edge := newPowers(t)

	if err := p.SetBudget(t.Context(), "$25.50", "week"); err != nil {
		t.Fatalf("SetBudget() error = %v", err)
	}

	b, _ := edge.Budget(t.Context())
	if b.TotalMicros != 25_500_000 || b.Period != domain.PeriodWeek {
		t.Fatalf("budget = %+v", b)
	}
}

func TestSetBudget_RefusesBadInputAndChangesNothing(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, amount, period string
		want                 error
	}{
		{"amount is not money", "ten", "week", domain.ErrAmount},
		{"period is unknown", "10", "year", domain.ErrPeriod},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p, edge := newPowers(t)

			if err := p.SetBudget(t.Context(), tt.amount, tt.period); !errors.Is(err, tt.want) {
				t.Fatalf("SetBudget() error = %v, want %v", err, tt.want)
			}
			if b, _ := edge.Budget(t.Context()); b.TotalMicros != 0 || b.Period != domain.PeriodMonth {
				t.Fatalf("budget changed: %+v", b)
			}
		})
	}
}

func TestSetDefence_ReadsTheShare(t *testing.T) {
	t.Parallel()
	p, edge := newPowers(t)

	if err := p.SetDefence(t.Context(), true, "20%"); err != nil {
		t.Fatalf("SetDefence() error = %v", err)
	}

	b, _ := edge.Budget(t.Context())
	if !b.Defence.Enabled || b.Defence.SharePct != 20 {
		t.Fatalf("defence = %+v", b.Defence)
	}
}

func TestSetDefence_RefusesABadShare(t *testing.T) {
	t.Parallel()
	p, edge := newPowers(t)

	if err := p.SetDefence(t.Context(), true, "150"); !errors.Is(err, domain.ErrShare) {
		t.Fatalf("SetDefence() error = %v, want %v", err, domain.ErrShare)
	}
	if b, _ := edge.Budget(t.Context()); b.Defence.Enabled {
		t.Fatal("a bad share switched the defence on")
	}
}

func TestFreezeAndRelease_FlipTheSwitch(t *testing.T) {
	t.Parallel()
	p, edge := newPowers(t)

	if err := p.Freeze(t.Context()); err != nil {
		t.Fatalf("Freeze() error = %v", err)
	}
	if s, _ := edge.Status(t.Context()); !s.Frozen || !s.Since.Equal(t0) {
		t.Fatalf("after freeze: %+v", s)
	}
	if err := p.Release(t.Context()); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	if s, _ := edge.Status(t.Context()); s.Frozen {
		t.Fatalf("after release: %+v", s)
	}
}
