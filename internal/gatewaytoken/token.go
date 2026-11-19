// SPDX-License-Identifier: MIT

// Package gatewaytoken defines the bearer token an agent's runner sends to the
// host model gateway, and signs and checks it.
//
// Format: ao1.<base64url(JSON claims)>.<base64url(HMAC-SHA256)>. Base64 is the
// URL alphabet without padding. The MAC covers the bytes "ao1." followed by
// the encoded claims, so the version prefix is bound to the signature.
//
// The HMAC key is the gateway key: the edge makes it and shares it read-only
// with the machine through the gateway-key volume. The daemon issues tokens;
// the gateway in the edge checks them.
//
// SOFT IDENTITY. Root inside the agent machine can read the gateway key and
// forge a token for any agent. A valid token therefore proves only that the
// caller is inside the agent machine, not which agent it is. The edge uses the
// claimed identity for
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

// MaxLifetime is the longest a token may live, from issue to expiry. The daemon issues tokens
// for 10 minutes by default.
const MaxLifetime = 15 * time.Minute

// SystemPrefix starts the name of every system caller, such as system:router.
const SystemPrefix = "system:"

// Kind is the kind of caller the token was issued to.
type Kind string

// Caller kinds. The agent kinds are as in the runner protocol; KindSystem is a daemon part that
// calls models itself, such as the router or the bid scorer.
const (
	KindAlwaysOn Kind = "always-on"
	KindChat     Kind = "chat"
	KindWaker    Kind = "waker"
	KindSystem   Kind = "system"
)

// ParseKind checks that s names a known caller kind.
func ParseKind(s string) (Kind, error) {
	switch k := Kind(s); k {
	case KindAlwaysOn, KindChat, KindWaker, KindSystem:
		return k, nil
	default:
		return "", fmt.Errorf("%w: unknown kind %q", ErrInvalidClaims, s)
	}
}

// Audience names the edge part a token is for. Each part accepts only its own audience, so a
// token minted for one purpose cannot be spent on another.
type Audience string

// Audiences.
const (
	// AudienceGateway tokens call models through the model gateway.
	AudienceGateway Audience = "gateway"
	// AudienceAudit tokens write to the edge audit sink; only system callers hold them.
	AudienceAudit Audience = "audit"
)

// Claims is what a token says about its holder. This identity is soft; see the
// package documentation.
type Claims struct {
	// Agent is the agent's name, which is its id. A system caller's name starts with
	// SystemPrefix.
	Agent string `json:"agent"`
	// Kind is always-on, chat or waker for an agent, system for a system caller.
	Kind Kind `json:"kind"`
	// Chart is the agent's full path in the org chart, such as founder/lead/dev. A system caller
	// has none.
	Chart string `json:"chart"`
	// Defence marks callers allowed to spend the defence slice of the budget.
	Defence bool `json:"defence"`
	// Audience is the edge part the token is for.
	Audience Audience `json:"aud"`
	// IssuedAt and ExpiresAt are Unix seconds.
	IssuedAt  int64 `json:"iat"`
	ExpiresAt int64 `json:"exp"`
}

func (c Claims) validate() error {
	if c.Agent == "" {
		return fmt.Errorf("%w: agent is empty", ErrInvalidClaims)
	}
	kind, err := ParseKind(string(c.Kind))
	if err != nil {
		return err
	}
	system := strings.HasPrefix(c.Agent, SystemPrefix)
	switch {
	case kind == KindSystem && (!system || c.Agent == SystemPrefix):
		return fmt.Errorf("%w: a system caller's name must start with %q", ErrInvalidClaims, SystemPrefix)
	case kind == KindSystem && c.Chart != "":
		return fmt.Errorf("%w: a system caller has no chart", ErrInvalidClaims)
	case kind != KindSystem && system:
		return fmt.Errorf("%w: only a system caller's name starts with %q", ErrInvalidClaims, SystemPrefix)
	case kind != KindSystem && c.Chart == "":
		return fmt.Errorf("%w: chart is empty", ErrInvalidClaims)
	}
	if c.Audience != AudienceGateway && c.Audience != AudienceAudit {
		return fmt.Errorf("%w: unknown audience %q", ErrInvalidClaims, c.Audience)
	}
	if c.IssuedAt <= 0 {
		return fmt.Errorf("%w: issue time missing", ErrInvalidClaims)
	}
	if c.ExpiresAt <= c.IssuedAt {
		return fmt.Errorf("%w: expiry not after issue time", ErrInvalidClaims)
	}
	if c.ExpiresAt-c.IssuedAt > int64(MaxLifetime/time.Second) {
		return fmt.Errorf("%w: lives longer than %v", ErrInvalidClaims, MaxLifetime)
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
