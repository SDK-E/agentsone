// SPDX-License-Identifier: MIT

package domain_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/auth/domain"
	"github.com/SDK-E/agentsone/internal/core"
)

var epoch = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func randomBytes(b byte) *bytes.Reader { return bytes.NewReader(bytes.Repeat([]byte{b}, 64)) }

func agent(name string) core.Caller {
	return core.Caller{Kind: core.CallerAgent, Name: name, UID: 0, IsRoot: true}
}

func TestNewToken_ReturnsAnOpaqueValueAndKeepsOnlyItsHash(t *testing.T) {
	t.Parallel()
	tok, value, err := domain.NewToken(randomBytes(7), agent("founder"), []string{"mcp:write"}, epoch, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(value, "aot_"+tok.ID+"_") {
		t.Fatalf("value %q does not carry id %q", value, tok.ID)
	}
	if strings.Contains(string(tok.Hash[:]), value) {
		t.Fatal("hash holds the secret")
	}
	if !tok.ExpiresAt.Equal(epoch.Add(time.Hour)) {
		t.Fatalf("expires %v", tok.ExpiresAt)
	}
}

func TestNewToken_RejectsBadScopes(t *testing.T) {
	t.Parallel()
	for _, s := range []string{"", "mcp", "mcp:own", "MCP:read", "mcp:read:x"} {
		if _, _, err := domain.NewToken(randomBytes(1), agent("a"), []string{s}, epoch, time.Hour); !errors.Is(err, core.ErrInvalid) {
			t.Errorf("scope %q: err %v, want ErrInvalid", s, err)
		}
	}
}

func TestNewToken_RejectsNonPositiveTTL(t *testing.T) {
	t.Parallel()
	if _, _, err := domain.NewToken(randomBytes(1), agent("a"), nil, epoch, 0); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("err %v", err)
	}
}

func TestNewToken_RejectsTTLOverTheMaximum(t *testing.T) {
	t.Parallel()
	if _, _, err := domain.NewToken(randomBytes(1), agent("a"), nil, epoch, domain.MaxTokenTTL+time.Second); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("err %v", err)
	}
}

func TestNewToken_FailsWhenRandomnessFails(t *testing.T) {
	t.Parallel()
	if _, _, err := domain.NewToken(bytes.NewReader(nil), agent("a"), nil, epoch, time.Hour); err == nil {
		t.Fatal("no error on empty random source")
	}
}

func TestToken_CheckAcceptsTheIssuedValueUntilExpiry(t *testing.T) {
	t.Parallel()
	tok, value, _ := domain.NewToken(randomBytes(2), agent("a"), []string{"bus:read"}, epoch, time.Hour)
	_, secret, err := domain.ParseTokenValue(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := tok.Check(secret, epoch.Add(59*time.Minute)); err != nil {
		t.Fatalf("valid token rejected: %v", err)
	}
	if err := tok.Check(secret, epoch.Add(time.Hour)); !errors.Is(err, core.ErrUnauthorized) {
		t.Fatalf("expired token: err %v", err)
	}
}

func TestToken_CheckRejectsAWrongSecret(t *testing.T) {
	t.Parallel()
	tok, _, _ := domain.NewToken(randomBytes(2), agent("a"), nil, epoch, time.Hour)
	if err := tok.Check("not-the-secret", epoch); !errors.Is(err, core.ErrUnauthorized) {
		t.Fatalf("err %v", err)
	}
}

func TestToken_CheckRejectsARevokedToken(t *testing.T) {
	t.Parallel()
	tok, value, _ := domain.NewToken(randomBytes(2), agent("a"), nil, epoch, time.Hour)
	_, secret, _ := domain.ParseTokenValue(value)
	tok.Revoke(epoch)
	if err := tok.Check(secret, epoch); !errors.Is(err, core.ErrUnauthorized) {
		t.Fatalf("err %v", err)
	}
}

func TestToken_CallerCarriesSubjectAndScopes(t *testing.T) {
	t.Parallel()
	tok, _, _ := domain.NewToken(randomBytes(2), core.Caller{Kind: core.CallerPlugin, Name: "notes", UID: -1}, []string{"memory:read"}, epoch, time.Hour)
	c := tok.Caller()
	if c.Kind != core.CallerPlugin || c.Name != "notes" || c.IsRoot || !c.Has("memory:read") || c.Has("memory:write") {
		t.Fatalf("caller %+v", c)
	}
}

func TestParseTokenValue_RejectsMalformedValues(t *testing.T) {
	t.Parallel()
	for _, v := range []string{"", "aot_", "aot_id", "xyz_id_secret", "aot__secret", "aot_id_"} {
		if _, _, err := domain.ParseTokenValue(v); !errors.Is(err, core.ErrUnauthorized) {
			t.Errorf("%q: err %v", v, err)
		}
	}
}

func TestToken_MayBeRevokedBy(t *testing.T) {
	t.Parallel()
	tok, _, _ := domain.NewToken(randomBytes(2), agent("scout"), nil, epoch, time.Hour)
	cases := []struct {
		name string
		by   core.Caller
		ok   bool
	}{
		{"any human", core.Caller{Kind: core.CallerHuman, Name: "ada"}, true},
		{"its own subject", core.Caller{Kind: core.CallerAgent, Name: "scout"}, true},
		{"another agent", core.Caller{Kind: core.CallerAgent, Name: "cto"}, false},
		{"anonymous", core.Anonymous, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := tok.MayBeRevokedBy(c.by); got != c.ok {
				t.Fatalf("got %v", got)
			}
		})
	}
}
