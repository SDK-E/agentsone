// SPDX-License-Identifier: MIT

package gatewaymint_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/core/coretest"
	"github.com/SDK-E/agentsone/internal/gatewaymint"
	"github.com/SDK-E/agentsone/internal/gatewaytoken"
)

var testKey = bytes.Repeat([]byte{0x5a}, gatewaymint.KeySize)

// keyFile writes key to a real file the way the edge's volume holds it.
func keyFile(t *testing.T, key []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "gateway-key", "key")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, key, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func minter(t *testing.T, path string) (*gatewaymint.Minter, *coretest.Fakes) {
	t.Helper()
	f, p := coretest.NewFakes()
	f.Agents.Add(core.Agent{Name: "founder", Kind: core.AgentAlwaysOn, IsRoot: true})
	f.Agents.Add(core.Agent{Name: "mira", Kind: core.AgentWaker, Manager: "founder", UID: 200001})
	return &gatewaymint.Minter{KeyFile: path, Clock: f.Clock, Ports: p}, f
}

// The gateway's own verifier accepts the token and reads the claims back.
func TestMint_SignsTokenTheGatewayVerifies(t *testing.T) {
	t.Parallel()
	m, f := minter(t, keyFile(t, testKey))
	tok, err := m.Mint(t.Context(), core.GatewayGrant{Subject: "mira", ChartPath: "founder/mira", Defence: true, TTL: 5 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	c, err := gatewaytoken.Verify(testKey, tok, f.Clock.Now())
	if err != nil {
		t.Fatalf("gateway verifier refused the token: %v", err)
	}
	want := gatewaytoken.Claims{Agent: "mira", Kind: gatewaytoken.KindWaker, Chart: "founder/mira", Defence: true,
		Audience: gatewaytoken.AudienceGateway,
		IssuedAt: f.Clock.Now().Unix(), ExpiresAt: f.Clock.Now().Add(5 * time.Minute).Unix()}
	if c != want {
		t.Fatalf("claims = %+v, want %+v", c, want)
	}
	if _, err := gatewaytoken.Verify(bytes.Repeat([]byte{1}, gatewaymint.KeySize), tok, f.Clock.Now()); !errors.Is(err, gatewaytoken.ErrSignature) {
		t.Errorf("another key verified the token: %v", err)
	}
	if _, err := gatewaytoken.Verify(testKey, tok, f.Clock.Now().Add(5*time.Minute)); !errors.Is(err, gatewaytoken.ErrExpired) {
		t.Errorf("token still valid at expiry: %v", err)
	}
}

// Daemon parts get kind system and no chart; each audience is signed into
// the token, so the gateway and the audit sink each accept only their own.
func TestMint_SystemCallersGetKindSystemAndTheirAudience(t *testing.T) {
	t.Parallel()
	m, f := minter(t, keyFile(t, testKey))
	now := f.Clock.Now()
	tests := map[string]struct {
		grant core.GatewayGrant
		want  gatewaytoken.Claims
	}{
		"router": {
			core.GatewayGrant{Subject: "system:router", TTL: 5 * time.Minute},
			gatewaytoken.Claims{Agent: "system:router", Kind: gatewaytoken.KindSystem, Chart: "",
				Audience: gatewaytoken.AudienceGateway, IssuedAt: now.Unix(), ExpiresAt: now.Add(5 * time.Minute).Unix()},
		},
		"audit shipper": {
			core.GatewayGrant{Subject: "system:audit", Audience: core.AudienceAudit, TTL: 5 * time.Minute},
			gatewaytoken.Claims{Agent: "system:audit", Kind: gatewaytoken.KindSystem, Chart: "",
				Audience: gatewaytoken.AudienceAudit, IssuedAt: now.Unix(), ExpiresAt: now.Add(5 * time.Minute).Unix()},
		},
	}
	for name, tt := range tests {
		tok, err := m.Mint(t.Context(), tt.grant)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		c, err := gatewaytoken.Verify(testKey, tok, now)
		if err != nil {
			t.Errorf("%s: gateway verifier refused the token: %v", name, err)
			continue
		}
		if c != tt.want {
			t.Errorf("%s: claims = %+v, want %+v", name, c, tt.want)
		}
	}
}

func TestMint_DefaultsToTenMinutesAndRefusesLongLives(t *testing.T) {
	t.Parallel()
	m, f := minter(t, keyFile(t, testKey))
	founder := core.GatewayGrant{Subject: "founder", ChartPath: "founder"}
	tok, err := m.Mint(t.Context(), founder)
	if err != nil {
		t.Fatal(err)
	}
	c, _ := gatewaytoken.Verify(testKey, tok, f.Clock.Now())
	if got := time.Duration(c.ExpiresAt-c.IssuedAt) * time.Second; got != gatewaymint.DefaultTTL {
		t.Errorf("default lifetime = %v, want %v", got, gatewaymint.DefaultTTL)
	}
	for _, ttl := range []time.Duration{-time.Minute, time.Millisecond, gatewaymint.MaxTTL + time.Second, 24 * time.Hour} {
		founder.TTL = ttl
		if _, err := m.Mint(t.Context(), founder); !errors.Is(err, core.ErrInvalid) {
			t.Errorf("ttl %v: error = %v, want ErrInvalid", ttl, err)
		}
	}
	founder.TTL = gatewaymint.MaxTTL
	if _, err := m.Mint(t.Context(), founder); err != nil {
		t.Errorf("ttl at the maximum refused: %v", err)
	}
}

// A missing or broken key is said plainly and matches ErrUnavailable.
func TestMint_WithoutKeySaysModelGatewayNotReachable(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	unreadable := filepath.Join(dir, "dir-not-file")
	if err := os.Mkdir(unreadable, 0o700); err != nil {
		t.Fatal(err)
	}
	tests := map[string]struct{ path, reason string }{
		"missing file": {filepath.Join(dir, "absent", "key"), "the gateway key file is missing"},
		"short key":    {keyFile(t, testKey[:16]), "the gateway key has 16 bytes, not 32"},
		"empty key":    {keyFile(t, nil), "the gateway key has 0 bytes, not 32"},
		"not readable": {unreadable, "the gateway key file cannot be read"},
	}
	for name, tt := range tests {
		m, _ := minter(t, tt.path)
		_, err := m.Mint(t.Context(), core.GatewayGrant{Subject: "mira", ChartPath: "founder/mira"})
		if !errors.Is(err, core.ErrUnavailable) || !errors.Is(err, gatewaytoken.ErrNoKey) {
			t.Errorf("%s: error = %v, want ErrUnavailable and ErrNoKey", name, err)
			continue
		}
		if msg := err.Error(); !strings.HasPrefix(msg, "model gateway not reachable: "+tt.reason) || !strings.Contains(msg, tt.path) {
			t.Errorf("%s: message = %q", name, msg)
		}
	}
}

func TestMint_RefusesSubjectsThatDoNotMatchTheirChartPath(t *testing.T) {
	t.Parallel()
	m, _ := minter(t, keyFile(t, testKey))
	tests := []core.GatewayGrant{
		{Subject: "mira"},
		{Subject: "mira", ChartPath: "founder/cto"},
		{Subject: "mira", ChartPath: "/founder/mira"},
		{Subject: "mira", ChartPath: "founder//mira"},
		{},
		{Subject: "system:router", ChartPath: "founder"},
		{Subject: "system:"},
		{Subject: "mira", ChartPath: "founder/mira", Audience: core.AudienceAudit},
		{Subject: "mira", ChartPath: "founder/mira", Audience: "web"},
	}
	for _, g := range tests {
		if _, err := m.Mint(t.Context(), g); !errors.Is(err, core.ErrInvalid) {
			t.Errorf("Mint(%+v) error = %v, want ErrInvalid", g, err)
		}
	}
	if _, err := m.Mint(t.Context(), core.GatewayGrant{Subject: "ghost", ChartPath: "founder/ghost"}); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("unknown agent: error = %v, want ErrNotFound", err)
	}
}

// An agent never gets kind system, whatever its directory entry says.
func TestMint_RefusesAgentsWithoutAnAgentKind(t *testing.T) {
	t.Parallel()
	m, f := minter(t, keyFile(t, testKey))
	f.Agents.Add(core.Agent{Name: "rogue", Kind: core.AgentKind(gatewaytoken.KindSystem), Manager: "founder"})
	f.Agents.Add(core.Agent{Name: "odd", Kind: core.AgentKind("robot"), Manager: "founder"})
	for _, name := range []string{"rogue", "odd"} {
		if _, err := m.Mint(t.Context(), core.GatewayGrant{Subject: name, ChartPath: "founder/" + name}); !errors.Is(err, core.ErrInvalid) {
			t.Errorf("%s: error = %v, want ErrInvalid", name, err)
		}
	}
}
