// SPDX-License-Identifier: MIT

// Package gatewaytoken defines the bearer token an agent's runner sends to the
// host model gateway, and signs and checks it.
//
// Format: ao1.<base64url(JSON claims)>.<base64url(HMAC-SHA256)>. Base64 is the
// URL alphabet without padding. The MAC covers the bytes "ao1." followed by
// the encoded claims, so the version prefix is bound to the signature.
//
// The HMAC key is a per-boot secret the host hands to the daemon. The daemon
// issues tokens; the gateway on the host checks them.
//
// SOFT IDENTITY. Root inside the VM can read the per-boot secret and forge a
// token for any agent. A valid token therefore proves only that the caller is
// inside the VM, not which agent it is. The host uses the claimed identity for
// soft walls (per-agent budget split, defence flag) and never for hard walls:
// the total budget is enforced whatever the token says.
package gatewaytoken

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Prefix starts every token and names the format version.
const Prefix = "ao1."

// ClockSkew is how far before its issue time a token is still accepted.
const ClockSkew = time.Minute

// Errors returned by Sign and Verify. Callers match them with errors.Is.
var (
	ErrNoKey         = errors.New("gatewaytoken: no signing key")
	ErrMalformed     = errors.New("gatewaytoken: malformed token")
	ErrSignature     = errors.New("gatewaytoken: bad signature")
	ErrExpired       = errors.New("gatewaytoken: token expired")
	ErrNotYetValid   = errors.New("gatewaytoken: token not yet valid")
	ErrInvalidClaims = errors.New("gatewaytoken: invalid claims")
)

// Kind is the kind of agent the token was issued to.
type Kind string

// Agent kinds, as in the runner protocol.
const (
	KindAlwaysOn Kind = "always-on"
	KindChat     Kind = "chat"
	KindWaker    Kind = "waker"
)

// ParseKind checks that s names a known agent kind.
func ParseKind(s string) (Kind, error) {
	switch k := Kind(s); k {
	case KindAlwaysOn, KindChat, KindWaker:
		return k, nil
	default:
		return "", fmt.Errorf("%w: unknown kind %q", ErrInvalidClaims, s)
	}
}

// Claims is what a token says about its holder. This identity is soft; see the
// package documentation.
type Claims struct {
	// Agent is the agent's name.
	Agent string `json:"agent"`
	// Kind is always-on, chat or waker.
	Kind Kind `json:"kind"`
	// Team is the chart branch the agent belongs to.
	Team string `json:"team"`
	// Defence marks callers allowed to spend the defence slice of the budget.
	Defence bool `json:"defence"`
	// IssuedAt and ExpiresAt are Unix seconds.
	IssuedAt  int64 `json:"iat"`
	ExpiresAt int64 `json:"exp"`
}

func (c Claims) validate() error {
	if c.Agent == "" {
		return fmt.Errorf("%w: agent is empty", ErrInvalidClaims)
	}
	if _, err := ParseKind(string(c.Kind)); err != nil {
		return err
	}
	if c.IssuedAt <= 0 {
		return fmt.Errorf("%w: issue time missing", ErrInvalidClaims)
	}
	if c.ExpiresAt <= c.IssuedAt {
		return fmt.Errorf("%w: expiry not after issue time", ErrInvalidClaims)
	}
	return nil
}

// Sign validates the claims and returns a signed token.
func Sign(key []byte, c Claims) (string, error) {
	if len(key) == 0 {
		return "", ErrNoKey
	}
	if err := c.validate(); err != nil {
		return "", err
	}
	return sign(key, c), nil
}

func sign(key []byte, c Claims) string {
	body, err := json.Marshal(c)
	if err != nil {
		// Claims holds only strings, bools and integers; Marshal cannot fail.
		panic(fmt.Sprintf("gatewaytoken: marshal claims: %v", err))
	}
	signed := Prefix + base64.RawURLEncoding.EncodeToString(body)
	return signed + "." + base64.RawURLEncoding.EncodeToString(mac(key, signed))
}

func mac(key []byte, signed string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(signed))
	return h.Sum(nil)
}

// Verify checks the token's form, signature, claims and validity window at now,
// and returns its claims.
func Verify(key []byte, token string, now time.Time) (Claims, error) {
	if len(key) == 0 {
		return Claims{}, ErrNoKey
	}
	if !strings.HasPrefix(token, Prefix) {
		return Claims{}, ErrMalformed
	}
	encClaims, encMAC, ok := strings.Cut(strings.TrimPrefix(token, Prefix), ".")
	if !ok || encClaims == "" || encMAC == "" || strings.Contains(encMAC, ".") {
		return Claims{}, ErrMalformed
	}
	body, err := base64.RawURLEncoding.DecodeString(encClaims)
	if err != nil {
		return Claims{}, ErrMalformed
	}
	got, err := base64.RawURLEncoding.DecodeString(encMAC)
	if err != nil {
		return Claims{}, ErrMalformed
	}
	if !hmac.Equal(got, mac(key, Prefix+encClaims)) {
		return Claims{}, ErrSignature
	}
	var c Claims
	dec := json.NewDecoder(strings.NewReader(string(body)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return Claims{}, ErrMalformed
	}
	if err := c.validate(); err != nil {
		return Claims{}, err
	}
	if now.Unix() >= c.ExpiresAt {
		return Claims{}, ErrExpired
	}
	if now.Add(ClockSkew).Unix() < c.IssuedAt {
		return Claims{}, ErrNotYetValid
	}
	return c, nil
}
