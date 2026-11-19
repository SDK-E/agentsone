// SPDX-License-Identifier: MIT

// Package gatewaymint is the daemon's one place to mint model gateway
// tokens (format ao1, internal/gatewaytoken) for runners, the router and
// the bid scorer. It signs with the per-boot gateway key the edge shares
// as a read-only file, and provides the core.GatewayTokens port.
package gatewaymint

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/gatewaytoken"
)

// Token lifetimes. The gateway refuses tokens that live longer than MaxTTL.
const (
	DefaultTTL = 10 * time.Minute
	MaxTTL     = gatewaytoken.MaxLifetime
)

// KeySize is the gateway key's length in bytes.
const KeySize = 32

// NoKeyError is a mint without a usable gateway key. It matches
// core.ErrUnavailable and gatewaytoken.ErrNoKey.
type NoKeyError struct {
	Path   string
	Reason string
}

func (e *NoKeyError) Error() string {
	return "model gateway not reachable: " + e.Reason + " (" + e.Path + ")"
}

// Is matches core.ErrUnavailable and gatewaytoken.ErrNoKey.
func (e *NoKeyError) Is(target error) bool {
	return target == core.ErrUnavailable || target == gatewaytoken.ErrNoKey
}

// Minter is core.GatewayTokens. It reads the key on every mint, so a key
// the edge mounts or rotates after boot is used at once.
type Minter struct {
	// KeyFile holds the raw gateway key.
	KeyFile string
	Clock   core.Clock
	// Ports gives the agent directory, for an agent subject's kind.
	Ports *core.Ports
}

var _ core.GatewayTokens = (*Minter)(nil)

// Mint signs a token for g; see core.GatewayTokens.
func (m *Minter) Mint(ctx context.Context, g core.GatewayGrant) (string, error) {
	if g.TTL == 0 {
		g.TTL = DefaultTTL
	}
	if g.TTL < time.Second || g.TTL > MaxTTL {
		return "", fmt.Errorf("%w: gateway token lifetime %v is not between 1s and %v", core.ErrInvalid, g.TTL, MaxTTL)
	}
	aud, err := audience(g)
	if err != nil {
		return "", err
	}
	kind, err := m.kind(ctx, g)
	if err != nil {
		return "", err
	}
	key, err := m.Key()
	if err != nil {
		return "", err
	}
	now := m.Clock.Now()
	return gatewaytoken.Sign(key, claims(g, kind, aud, now))
}

// audience maps the grant's audience to the token's. Only daemon subjects
// get audit tokens.
func audience(g core.GatewayGrant) (gatewaytoken.Audience, error) {
	switch g.Audience {
	case "", core.AudienceGateway:
		return gatewaytoken.AudienceGateway, nil
	case core.AudienceAudit:
		if !strings.HasPrefix(g.Subject, gatewaytoken.SystemPrefix) {
			return "", fmt.Errorf("%w: only daemon subjects get audit tokens, not %q", core.ErrInvalid, g.Subject)
		}
		return gatewaytoken.AudienceAudit, nil
	default:
		return "", fmt.Errorf("%w: unknown gateway token audience %q", core.ErrInvalid, g.Audience)
	}
}

func (m *Minter) kind(ctx context.Context, g core.GatewayGrant) (gatewaytoken.Kind, error) {
	if strings.HasPrefix(g.Subject, gatewaytoken.SystemPrefix) {
		if g.Subject == gatewaytoken.SystemPrefix || g.ChartPath != "" {
			return "", fmt.Errorf("%w: daemon subject %q needs a name and no chart path", core.ErrInvalid, g.Subject)
		}
		return gatewaytoken.KindSystem, nil
	}
	parts := strings.Split(g.ChartPath, "/")
	if g.Subject == "" || parts[len(parts)-1] != g.Subject || strings.Contains(g.ChartPath, "//") || strings.HasPrefix(g.ChartPath, "/") {
		return "", fmt.Errorf("%w: chart path %q does not end with agent %q", core.ErrInvalid, g.ChartPath, g.Subject)
	}
	a, err := m.Ports.Agents.GetAgent(ctx, g.Subject)
	if err != nil {
		return "", fmt.Errorf("gateway token for %s: %w", g.Subject, err)
	}
	kind, err := gatewaytoken.ParseKind(string(a.Kind))
	if err != nil || kind == gatewaytoken.KindSystem {
		return "", fmt.Errorf("%w: agent %q has kind %q, not an agent kind", core.ErrInvalid, g.Subject, a.Kind)
	}
	return kind, nil
}

// Key reads the gateway key. Without one the error is a *NoKeyError.
func (m *Minter) Key() ([]byte, error) {
	key, err := os.ReadFile(m.KeyFile)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return nil, &NoKeyError{Path: m.KeyFile, Reason: "the gateway key file is missing"}
	case err != nil:
		return nil, &NoKeyError{Path: m.KeyFile, Reason: "the gateway key file cannot be read"}
	case len(key) != KeySize:
		return nil, &NoKeyError{Path: m.KeyFile, Reason: fmt.Sprintf("the gateway key has %d bytes, not %d", len(key), KeySize)}
	}
	return key, nil
}

// claims maps a grant to token claims.
func claims(g core.GatewayGrant, kind gatewaytoken.Kind, aud gatewaytoken.Audience, now time.Time) gatewaytoken.Claims {
	return gatewaytoken.Claims{Agent: g.Subject, Kind: kind, Chart: g.ChartPath, Defence: g.Defence,
		Audience: aud, IssuedAt: now.Unix(), ExpiresAt: now.Add(g.TTL).Unix()}
}
