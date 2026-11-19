// SPDX-License-Identifier: MIT

package coretest

import (
	"context"
	"sync"

	"github.com/SDK-E/agentsone/internal/core"
)

// GatewayTokens is a fake core.GatewayTokens. It returns readable fake
// tokens, or Err when set.
type GatewayTokens struct {
	mu     sync.Mutex
	grants []core.GatewayGrant
	// Err, when set, is returned by every Mint.
	Err error
}

var _ core.GatewayTokens = (*GatewayTokens)(nil)

// NewGatewayTokens returns a fake that mints "fake-gateway-token-<subject>".
func NewGatewayTokens() *GatewayTokens { return &GatewayTokens{} }

// Mint records g and returns a fake token.
func (g *GatewayTokens) Mint(_ context.Context, grant core.GatewayGrant) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.Err != nil {
		return "", g.Err
	}
	g.grants = append(g.grants, grant)
	return "fake-gateway-token-" + grant.Subject, nil
}

// Grants returns the recorded grants in order.
func (g *GatewayTokens) Grants() []core.GatewayGrant {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]core.GatewayGrant(nil), g.grants...)
}
