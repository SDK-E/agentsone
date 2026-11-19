// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"errors"
	"strings"

	"github.com/SDK-E/agentsone/internal/edge/control/domain"
)

// Errors of the power inputs.
var (
	ErrEmptyName = errors.New("write the name to allow, like example.org")
	ErrEmptyKey  = errors.New("paste the key first")
)

// PowersOptions are the edge ports.
type PowersOptions struct {
	Network Network
	Models  Models
	Money   Money
	Freeze  Freeze
}

// Powers are the human-only powers. Each one is enforced by the edge, outside the machine, so an
// agent with root in the machine cannot change them.
type Powers struct {
	net    Network
	models Models
	money  Money
	freeze Freeze
}

// NewPowers returns the powers.
func NewPowers(o PowersOptions) *Powers {
	return &Powers{net: o.Network, models: o.Models, money: o.Money, freeze: o.Freeze}
}

// Overview is every power's current state. Each part carries its own error, so one part the
// edge cannot give does not hide the others.
type Overview struct {
	Allow        []AllowEntry
	AllowErr     error
	Providers    []Provider
	ProvidersErr error
	Budget       Budget
	BudgetErr    error
	Freeze       FreezeState
	FreezeErr    error
}

// Overview reads every power.
func (p *Powers) Overview(ctx context.Context) Overview {
	var o Overview
	o.Freeze, o.FreezeErr = p.freeze.Status(ctx)
	o.Allow, o.AllowErr = p.net.AllowList(ctx)
	o.Providers, o.ProvidersErr = p.models.Providers(ctx)
	o.Budget, o.BudgetErr = p.money.Budget(ctx)
	return o
}

// Allow lets the machine reach a name.
func (p *Powers) Allow(ctx context.Context, pattern, why string) error {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return ErrEmptyName
	}
	_, err := p.net.Allow(ctx, pattern, strings.TrimSpace(why))
	return err
}

// Disallow takes a name off the allow list.
func (p *Powers) Disallow(ctx context.Context, pattern string) error {
	return p.net.Disallow(ctx, pattern)
}

// SetKey gives a provider its key. Only the fingerprint comes back.
func (p *Powers) SetKey(ctx context.Context, provider, key string) (KeyState, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return KeyState{}, ErrEmptyKey
	}
	return p.models.SetKey(ctx, provider, key)
}

// ClearKey removes a provider's key.
func (p *Powers) ClearKey(ctx context.Context, provider string) error {
	return p.models.ClearKey(ctx, provider)
}

// Enable lets the gateway use a provider.
func (p *Powers) Enable(ctx context.Context, provider string) error {
	return p.models.Enable(ctx, provider)
}

// Disable stops the gateway using a provider.
func (p *Powers) Disable(ctx context.Context, provider string) error {
	return p.models.Disable(ctx, provider)
}

// SetBudget sets the total budget, in dollars, and how often it starts again.
func (p *Powers) SetBudget(ctx context.Context, amount, period string) error {
	micros, err := domain.ParseDollars(amount)
	if err != nil {
		return err
	}
	per, err := domain.ParsePeriod(period)
	if err != nil {
		return err
	}
	_, err = p.money.SetBudget(ctx, BudgetChange{TotalMicros: &micros, Period: &per})
	return err
}

// SetDefence switches the defence share on or off and sets its percentage.
func (p *Powers) SetDefence(ctx context.Context, enabled bool, share string) error {
	pct, err := domain.ParseShare(share)
	if err != nil {
		return err
	}
	_, err = p.money.SetDefence(ctx, DefenceChange{Enabled: &enabled, SharePct: &pct})
	return err
}

// Freeze freezes all: the edge cuts every model call and all internet traffic of the machine.
func (p *Powers) Freeze(ctx context.Context) error { return p.freeze.Freeze(ctx) }

// Release ends the freeze.
func (p *Powers) Release(ctx context.Context) error { return p.freeze.Release(ctx) }
