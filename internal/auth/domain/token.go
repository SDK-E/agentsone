// SPDX-License-Identifier: MIT

package domain

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/SDK-E/agentsone/internal/core"
)

// MaxTokenTTL is the longest life a token may have.
const MaxTokenTTL = 30 * 24 * time.Hour

const tokenPrefix = "aot_"

// scopePattern is the contract's scope form <group>:<read|write|admin>.
// Fixed lookup table.
var scopePattern = regexp.MustCompile(`^[a-z][a-z0-9-]*:(read|write|admin)$`)

// Token is an issued bearer token. Only the hash of its secret is kept.
type Token struct {
	ID        string
	Hash      [sha256.Size]byte
	Subject   core.Caller
	Scopes    []string
	IssuedAt  time.Time
	ExpiresAt time.Time
	// RevokedAt is zero while the token is live.
	RevokedAt time.Time
}

// ValidateGrant checks what a token would grant: a lifetime in
// (0, MaxTokenTTL] and scopes of the contract's form. An error matches
// core.ErrInvalid. Test fakes of core.TokenIssuer call it too, so they
// refuse what the real issuer refuses.
func ValidateGrant(scopes []string, ttl time.Duration) error {
	if ttl <= 0 || ttl > MaxTokenTTL {
		return fmt.Errorf("token ttl %s outside (0, %s]: %w", ttl, MaxTokenTTL, core.ErrInvalid)
	}
	for _, s := range scopes {
		if !scopePattern.MatchString(s) {
			return fmt.Errorf("token scope %q: %w", s, core.ErrInvalid)
		}
	}
	return nil
}

// NewToken makes a token for subject and returns it with the one-time value
// "aot_<id>_<secret>" to hand to the holder. rand is crypto/rand in production.
func NewToken(rand io.Reader, subject core.Caller, scopes []string, now time.Time, ttl time.Duration) (Token, string, error) {
	if err := ValidateSubject(subject); err != nil {
		return Token{}, "", err
	}
	if err := ValidateGrant(scopes, ttl); err != nil {
		return Token{}, "", err
	}
	// The id is hex so it never holds the "_" separator; the secret may.
	idBytes := make([]byte, 12)
	if _, err := io.ReadFull(rand, idBytes); err != nil {
		return Token{}, "", fmt.Errorf("token id: %w", err)
	}
	id := hex.EncodeToString(idBytes)
	secret, err := randomText(rand, 32)
	if err != nil {
		return Token{}, "", fmt.Errorf("token secret: %w", err)
	}
	subject.Scopes = nil
	t := Token{
		ID:        id,
		Hash:      sha256.Sum256([]byte(secret)),
		Subject:   subject,
		Scopes:    append([]string(nil), scopes...),
		IssuedAt:  now,
		ExpiresAt: now.Add(ttl),
	}
	return t, tokenPrefix + id + "_" + secret, nil
}

// ParseTokenValue splits a presented value into id and secret.
func ParseTokenValue(value string) (id, secret string, err error) {
	rest, ok := strings.CutPrefix(value, tokenPrefix)
	if !ok {
		return "", "", fmt.Errorf("token format: %w", core.ErrUnauthorized)
	}
	id, secret, ok = strings.Cut(rest, "_")
	if !ok || id == "" || secret == "" {
		return "", "", fmt.Errorf("token format: %w", core.ErrUnauthorized)
	}
	return id, secret, nil
}

// Check accepts secret when it matches, the token is not revoked and now is
// before expiry. The comparison is constant time.
func (t Token) Check(secret string, now time.Time) error {
	sum := sha256.Sum256([]byte(secret))
	if subtle.ConstantTimeCompare(sum[:], t.Hash[:]) != 1 {
		return fmt.Errorf("token %s: %w", t.ID, core.ErrUnauthorized)
	}
	if !t.RevokedAt.IsZero() || !now.Before(t.ExpiresAt) {
		return fmt.Errorf("token %s expired or revoked: %w", t.ID, core.ErrUnauthorized)
	}
	return nil
}

// Revoke ends the token at now.
func (t *Token) Revoke(now time.Time) {
	if t.RevokedAt.IsZero() {
		t.RevokedAt = now
	}
}

// tokenSubject reports whether a token may name kind. The daemon's own
// (system) and the edge's (host) identities come only from the transport,
// so a bearer token, which anyone holding it presents, never carries them.
func tokenSubject(kind core.CallerKind) bool {
	return kind == core.CallerAgent || kind == core.CallerHuman || kind == core.CallerPlugin
}

// ValidateSubject refuses a token for a subject no token may name, with
// core.ErrForbidden. Test fakes of core.TokenIssuer call it too.
func ValidateSubject(subject core.Caller) error {
	if !tokenSubject(subject.Kind) {
		return fmt.Errorf("token for a %q caller: %w", subject.Kind, core.ErrForbidden)
	}
	return nil
}

// Caller is the identity a request with this token gets. A token never
// carries root, the system or the edge: its power is its scopes. A token
// stored for another kind of subject grants nothing.
func (t Token) Caller() core.Caller {
	if !tokenSubject(t.Subject.Kind) {
		return core.Anonymous
	}
	c := t.Subject
	c.IsRoot = false
	c.UID = -1
	c.Scopes = append([]string(nil), t.Scopes...)
	// A token proves who holds it, not where the process runs.
	c.Assurance = core.AssuranceSoft
	return c
}

// MayBeRevokedBy says who may revoke: any human, or the token's own subject.
func (t Token) MayBeRevokedBy(c core.Caller) bool {
	if c.Kind == core.CallerHuman && c.Name != "" {
		return true
	}
	return c.Kind == t.Subject.Kind && c.Name == t.Subject.Name && c.Name != ""
}

func randomText(rand io.Reader, n int) (string, error) {
	b := make([]byte, n)
	if _, err := io.ReadFull(rand, b); err != nil {
		return "", fmt.Errorf("read random: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
