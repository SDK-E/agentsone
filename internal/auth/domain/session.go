// SPDX-License-Identifier: MIT

package domain

import (
	"crypto/sha256"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/SDK-E/agentsone/internal/core"
)

// SessionTTL is how long a human stays signed in.
const SessionTTL = 12 * time.Hour

// humanName is the contract's HumanName pattern. Fixed lookup table.
var humanName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,30}$`)

// Human is a human account, kept in core.db. Humans are not Linux users:
// the container's /etc resets on every restart.
type Human struct {
	Name        string
	DisplayName string
	// UID is a stable account number from 1000 up, shown in the contract's
	// Human.uid. It is not a Linux uid.
	UID int
	// PasswordHash is an argon2id PHC string. It never leaves this context.
	PasswordHash string
	CreatedAt   time.Time
	// LastLoginAt is zero before the first sign-in.
	LastLoginAt time.Time
}

// CheckHumanName rejects names outside the contract pattern.
func CheckHumanName(name string) error {
	if !humanName.MatchString(name) {
		return fmt.Errorf("human name %q: %w", name, core.ErrInvalid)
	}
	return nil
}

// Caller is the identity of a request signed in as h.
func (h Human) Caller() core.Caller {
	return core.Caller{Kind: core.CallerHuman, Name: h.Name, UID: -1}
}

// Session is a server-side sign-in. The cookie holds a random secret; only
// its hash is stored, so a database leak does not leak live sessions.
type Session struct {
	// ID is the hex SHA-256 of the cookie secret.
	ID        string
	Human     string
	CreatedAt time.Time
	ExpiresAt time.Time
}

// SessionPrefix starts every session cookie value, so a bearer check can
// tell a session from a token.
const SessionPrefix = "aos_"

// NewSession starts a session for human and returns the cookie secret.
func NewSession(rand io.Reader, human string, now time.Time) (Session, string, error) {
	secret, err := randomText(rand, 32)
	if err != nil {
		return Session{}, "", fmt.Errorf("session secret: %w", err)
	}
	secret = SessionPrefix + secret
	return Session{ID: SessionID(secret), Human: human, CreatedAt: now, ExpiresAt: now.Add(SessionTTL)}, secret, nil
}

// IsSessionValue reports whether a presented credential is a session cookie value.
func IsSessionValue(v string) bool { return strings.HasPrefix(v, SessionPrefix) }

// SessionID is the stored key for a cookie secret.
func SessionID(secret string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(secret)))
}

// Live reports whether the session is still valid at now.
func (s Session) Live(now time.Time) bool { return now.Before(s.ExpiresAt) }
