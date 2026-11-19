// SPDX-License-Identifier: MIT

// Package app holds the use cases of the control page: set up and sign in with the human-only
// passphrase, and use the human-only powers the edge enforces. The edge is reached only through
// the ports below; the adapter speaks to the edge admin socket.
package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/SDK-E/agentsone/internal/edge/control/domain"
)

// Clock tells the time.
type Clock interface {
	Now() time.Time
}

// Errors of the credential port.
var (
	// ErrNoCredential: no passphrase is set yet.
	ErrNoCredential = errors.New("no passphrase is set")
	// ErrCredentialSet: a passphrase is already set.
	ErrCredentialSet = errors.New("a passphrase is already set")
)

// Credentials is the passphrase hash, kept by the edge in its own volume. The control service
// has a read-only disk and no volume, so it keeps nothing itself.
type Credentials interface {
	// Get returns the stored hash, or ErrNoCredential.
	Get(ctx context.Context) (string, error)
	// Create stores the first hash, or returns ErrCredentialSet.
	Create(ctx context.Context, hash string) error
}

// Hasher turns a passphrase into a slow, salted hash and checks one.
type Hasher interface {
	Hash(pass string) (string, error)
	Verify(hash, pass string) (bool, error)
}

// Refusal is the edge saying no to an input, with a reason a person can read: a bad pattern, a
// duplicate entry, an unknown provider.
type Refusal struct {
	Detail string
}

func (r *Refusal) Error() string { return r.Detail }

// ErrEdgeUnavailable means the edge did not answer, or failed. The page says so; the edge log
// has the reason.
var ErrEdgeUnavailable = errors.New("the edge did not answer")

// AllowEntry is one name the machine may reach.
type AllowEntry struct {
	Pattern string
	Why     string
}

// Network is the internet allow list, enforced by the edge.
type Network interface {
	AllowList(ctx context.Context) ([]AllowEntry, error)
	Allow(ctx context.Context, pattern, why string) (AllowEntry, error)
	Disallow(ctx context.Context, pattern string) error
}

// KeyState says whether a provider has a key. The key itself never comes back.
type KeyState struct {
	Set bool
	// Fingerprint is the first 8 hex digits of the key's SHA-256, so the human can tell keys apart.
	Fingerprint string
}

// Model is one model a provider offers.
type Model struct {
	ID   string
	Tier string
}

// Provider is one model provider the gateway knows.
type Provider struct {
	ID          string
	Enabled     bool
	KeyRequired bool
	Key         KeyState
	Models      []Model
}

// Models is the providers and their keys, held by the edge.
type Models interface {
	Providers(ctx context.Context) ([]Provider, error)
	Enable(ctx context.Context, provider string) error
	Disable(ctx context.Context, provider string) error
	SetKey(ctx context.Context, provider, key string) (KeyState, error)
	ClearKey(ctx context.Context, provider string) error
}

// Defence is the share of the budget kept for wakers and security work.
type Defence struct {
	Enabled         bool
	SharePct        int
	ReservedMicros  int64
	RemainingMicros int64
}

// Budget is the human's total model budget, enforced by the edge.
type Budget struct {
	Currency        string
	TotalMicros     int64
	SpentMicros     int64
	ReservedMicros  int64
	RemainingMicros int64
	Exhausted       bool
	Period          domain.Period
	PeriodStart     time.Time
	PeriodEnd       time.Time
	Defence         Defence
}

// BudgetChange changes the total, the period or both; nil leaves a field as it is.
type BudgetChange struct {
	TotalMicros *int64
	Period      *domain.Period
}

// DefenceChange changes the defence share; nil leaves a field as it is.
type DefenceChange struct {
	Enabled  *bool
	SharePct *int
}

// Money is the total budget and the defence share.
type Money interface {
	Budget(ctx context.Context) (Budget, error)
	SetBudget(ctx context.Context, c BudgetChange) (Budget, error)
	SetDefence(ctx context.Context, c DefenceChange) (Budget, error)
}

// FreezeState is the freeze-all switch.
type FreezeState struct {
	Frozen bool
	// Since is when the switch last changed; zero when it never did.
	Since time.Time
}

// Freeze is the freeze-all switch: on, the edge cuts every model call and all internet traffic
// of the machine.
type Freeze interface {
	Status(ctx context.Context) (FreezeState, error)
	Freeze(ctx context.Context) error
	Release(ctx context.Context) error
}

// WaitError means too many failed tries: the next is allowed after Wait.
type WaitError struct {
	Wait time.Duration
}

func (e *WaitError) Error() string {
	return fmt.Sprintf("too many tries, wait %s", e.Wait.Round(time.Second))
}
