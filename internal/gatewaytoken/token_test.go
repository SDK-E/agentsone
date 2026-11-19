// SPDX-License-Identifier: MIT

package gatewaytoken_test

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/gatewaytoken"
)

var (
	testKey  = []byte("test-boot-secret-0123456789abcdef")
	otherKey = []byte("other-boot-secret-0123456789abcd")
	issued   = time.Date(2026, 11, 19, 20, 0, 0, 0, time.UTC)
)

func validClaims() gatewaytoken.Claims {
	return gatewaytoken.Claims{
		Agent:     "founder",
		Kind:      gatewaytoken.KindAlwaysOn,
		Team:      "founder",
		Defence:   false,
		IssuedAt:  issued.Unix(),
		ExpiresAt: issued.Add(time.Hour).Unix(),
	}
}

func TestSignThenVerifyReturnsSameClaims(t *testing.T) {
	want := validClaims()
	want.Defence = true
	want.Kind = gatewaytoken.KindWaker

	tok, err := gatewaytoken.Sign(testKey, want)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if !strings.HasPrefix(tok, "ao1.") || strings.Count(tok, ".") != 2 {
		t.Fatalf("token %q does not have the form ao1.<claims>.<mac>", tok)
	}

	got, err := gatewaytoken.Verify(testKey, tok, issued.Add(time.Minute))
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if got != want {
		t.Fatalf("claims = %+v, want %+v", got, want)
	}
}

func TestVerifyRefuses(t *testing.T) {
	good, err := gatewaytoken.Sign(testKey, validClaims())
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	parts := strings.Split(good, ".")
	forged := base64.RawURLEncoding.EncodeToString(
		[]byte(`{"agent":"founder","kind":"always-on","team":"founder","defence":true,"iat":1,"exp":99999999999}`))

	tests := []struct {
		name  string
		key   []byte
		token string
		now   time.Time
		want  error
	}{
		{"empty", testKey, "", issued, gatewaytoken.ErrMalformed},
		{"wrong prefix", testKey, "ao2." + parts[1] + "." + parts[2], issued, gatewaytoken.ErrMalformed},
		{"two parts", testKey, "ao1." + parts[1], issued, gatewaytoken.ErrMalformed},
		{"bad base64 claims", testKey, "ao1.!!!." + parts[2], issued, gatewaytoken.ErrMalformed},
		{"bad base64 mac", testKey, "ao1." + parts[1] + ".!!!", issued, gatewaytoken.ErrMalformed},
		{"wrong key", otherKey, good, issued, gatewaytoken.ErrSignature},
		{"claims swapped", testKey, "ao1." + forged + "." + parts[2], issued, gatewaytoken.ErrSignature},
		{"expired", testKey, good, issued.Add(time.Hour), gatewaytoken.ErrExpired},
		{"not yet valid", testKey, good, issued.Add(-2 * time.Minute), gatewaytoken.ErrNotYetValid},
		{"no key", nil, good, issued, gatewaytoken.ErrNoKey},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := gatewaytoken.Verify(tc.key, tc.token, tc.now)
			if !errors.Is(err, tc.want) {
				t.Fatalf("Verify error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestVerifyAllowsSmallClockSkewAtIssue(t *testing.T) {
	tok, err := gatewaytoken.Sign(testKey, validClaims())
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if _, err := gatewaytoken.Verify(testKey, tok, issued.Add(-30*time.Second)); err != nil {
		t.Fatalf("Verify within skew: %v", err)
	}
}

func TestSignRejectsInvalidClaims(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*gatewaytoken.Claims)
	}{
		{"no agent", func(c *gatewaytoken.Claims) { c.Agent = "" }},
		{"unknown kind", func(c *gatewaytoken.Claims) { c.Kind = "robot" }},
		{"expiry before issue", func(c *gatewaytoken.Claims) { c.ExpiresAt = c.IssuedAt }},
		{"no issue time", func(c *gatewaytoken.Claims) { c.IssuedAt = 0 }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := validClaims()
			tc.mutate(&c)
			if _, err := gatewaytoken.Sign(testKey, c); !errors.Is(err, gatewaytoken.ErrInvalidClaims) {
				t.Fatalf("Sign error = %v, want ErrInvalidClaims", err)
			}
		})
	}
	if _, err := gatewaytoken.Sign(nil, validClaims()); !errors.Is(err, gatewaytoken.ErrNoKey) {
		t.Fatalf("Sign without key error = %v, want ErrNoKey", err)
	}
}

func TestVerifyRefusesSignedButInvalidClaims(t *testing.T) {
	// A token whose MAC is right but whose claims break the rules (unknown kind)
	// must still be refused.
	c := validClaims()
	c.Kind = "robot"
	tok := gatewaytoken.SignUnchecked(testKey, c)
	if _, err := gatewaytoken.Verify(testKey, tok, issued); !errors.Is(err, gatewaytoken.ErrInvalidClaims) {
		t.Fatalf("Verify error = %v, want ErrInvalidClaims", err)
	}
}

func TestParseKind(t *testing.T) {
	for _, k := range []string{"always-on", "chat", "waker"} {
		if _, err := gatewaytoken.ParseKind(k); err != nil {
			t.Fatalf("ParseKind(%q): %v", k, err)
		}
	}
	if _, err := gatewaytoken.ParseKind("x"); err == nil {
		t.Fatal("ParseKind(x) accepted an unknown kind")
	}
}
