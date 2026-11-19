// SPDX-License-Identifier: MIT

// Package adminapi is the edge admin API: the request and response types of the routes the edge
// serves on its admin socket, and a client for them. The control container uses the client; the
// edge parts use the types to serve the routes.
//
// The admin socket is a Unix socket, mode 0660, in a volume only the edge and control containers
// mount. Every error is an RFC 9457 problem document typed urn:agentsone:problem:<slug>.
//
//	GET    /v1/gateway/providers            ProviderList
//	PUT    /v1/gateway/providers/{id}       add (enable) a provider -> Provider
//	DELETE /v1/gateway/providers/{id}       remove (disable) a provider -> 204
//	PUT    /v1/gateway/providers/{id}/key   SetKeyRequest -> KeyState
//	DELETE /v1/gateway/providers/{id}/key   clear the key -> 204
//	GET    /v1/gateway/budget               Budget
//	PATCH  /v1/gateway/budget               SetBudgetRequest -> Budget
//	PATCH  /v1/gateway/budget/defence       SetDefenceRequest -> Budget
//	GET    /v1/net/allow-list               AllowList
//	POST   /v1/net/allow-list               AllowEntry -> 201 AllowEntry
//	DELETE /v1/net/allow-list/{pattern}     204
//	GET    /v1/net/freeze                   EgressFreeze
//
// Provider keys are write-only: no route returns a key, only whether one is set and a short
// fingerprint (the first 8 hex digits of its SHA-256) to tell keys apart.
//
// Owned by work package 16 (edge: gateway, budget, audit sink, web door).
package adminapi

import (
	"crypto/sha256"
	"encoding/hex"
	"time"
)

// Route patterns, for http.ServeMux.
const (
	RouteProviders      = "GET /v1/gateway/providers"
	RouteAddProvider    = "PUT /v1/gateway/providers/{id}"
	RouteRemoveProvider = "DELETE /v1/gateway/providers/{id}"
	RouteSetKey         = "PUT /v1/gateway/providers/{id}/key"
	RouteClearKey       = "DELETE /v1/gateway/providers/{id}/key"
	RouteBudget         = "GET /v1/gateway/budget"
	RouteSetBudget      = "PATCH /v1/gateway/budget"
	RouteSetDefence     = "PATCH /v1/gateway/budget/defence"
	RouteAllowList      = "GET /v1/net/allow-list"
	RouteAddAllowed     = "POST /v1/net/allow-list"
	RouteRemoveAllowed  = "DELETE /v1/net/allow-list/{pattern}"
	RouteEgressFreeze   = "GET /v1/net/freeze"
)

// Model is one model a provider serves.
type Model struct {
	ID   string `json:"id"`
	Tier string `json:"tier"`
}

// KeyState tells whether a provider key is set, never the key.
type KeyState struct {
	Set bool `json:"set"`
	// Fingerprint is the first 8 hex digits of the key's SHA-256; empty when no key is set.
	Fingerprint string `json:"fingerprint,omitempty"`
}

// Provider is one model provider known to the gateway.
type Provider struct {
	ID          string   `json:"id"`
	Enabled     bool     `json:"enabled"`
	KeyRequired bool     `json:"key_required"`
	Key         KeyState `json:"key"`
	Models      []Model  `json:"models"`
}

// ProviderList answers GET /v1/gateway/providers.
type ProviderList struct {
	Providers []Provider `json:"providers"`
}

// SetKeyRequest stores a provider key.
type SetKeyRequest struct {
	Key string `json:"key"`
}

// Defence is the defence slice of the budget.
type Defence struct {
	Enabled         bool  `json:"enabled"`
	SharePct        int   `json:"share_pct"`
	ReservedMicros  int64 `json:"reserved_micros"`
	RemainingMicros int64 `json:"remaining_micros"`
}

// Budget is the hard total budget and what is left of it in the current period.
type Budget struct {
	Currency        string    `json:"currency"`
	TotalMicros     int64     `json:"total_micros"`
	SpentMicros     int64     `json:"spent_micros"`
	ReservedMicros  int64     `json:"reserved_micros"`
	RemainingMicros int64     `json:"remaining_micros"`
	Exhausted       bool      `json:"exhausted"`
	Period          string    `json:"period"`
	PeriodStart     time.Time `json:"period_start"`
	PeriodEnd       time.Time `json:"period_end"`
	Defence         Defence   `json:"defence"`
}

// The budget periods.
const (
	PeriodMonth = "month"
	PeriodWeek  = "week"
	PeriodDay   = "day"
)

// SetBudgetRequest changes the total, the period or both; a nil field is left as it is.
type SetBudgetRequest struct {
	TotalMicros *int64  `json:"total_micros,omitempty"`
	Period      *string `json:"period,omitempty"`
}

// SetDefenceRequest changes the defence slice; a nil field is left as it is.
type SetDefenceRequest struct {
	Enabled  *bool `json:"enabled,omitempty"`
	SharePct *int  `json:"share_pct,omitempty"`
}

// AllowEntry is one allow list entry.
type AllowEntry struct {
	Pattern string `json:"pattern"`
	Why     string `json:"why"`
}

// AllowList answers GET /v1/net/allow-list.
type AllowList struct {
	Entries []AllowEntry `json:"entries"`
}

// EgressFreeze answers GET /v1/net/freeze.
type EgressFreeze struct {
	Frozen bool `json:"frozen"`
}

// Fingerprint is the short fingerprint of a key.
func Fingerprint(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:4])
}
