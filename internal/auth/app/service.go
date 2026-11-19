// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/SDK-E/agentsone/internal/auth/domain"
	"github.com/SDK-E/agentsone/internal/core"
)

// ErrTooManyAttempts is returned while a name is locked by the sign-in throttle.
var ErrTooManyAttempts = errors.New("too many sign-in attempts")

// TooManyAttempts carries how long to wait.
type TooManyAttempts struct{ RetryAfter time.Duration }

func (e *TooManyAttempts) Error() string {
	return fmt.Sprintf("%s: retry after %s", ErrTooManyAttempts, e.RetryAfter)
}

// Unwrap makes errors.Is(err, ErrTooManyAttempts) work.
func (e *TooManyAttempts) Unwrap() error { return ErrTooManyAttempts }

// Service holds the identity use cases: sign-in sessions, tokens, human
// accounts and first-boot setup.
type Service struct {
	d        Deps
	mu       sync.Mutex
	throttle *domain.Throttle
}

var _ core.TokenIssuer = (*Service)(nil)

// New builds the service.
func New(d Deps) *Service { return &Service{d: d, throttle: domain.NewThrottle()} }

// SignIn checks name and password and starts a session. It returns the
// cookie secret, which is never stored.
func (s *Service) SignIn(ctx context.Context, name, password, remote string) (domain.Session, string, domain.Human, error) {
	now := s.d.Clock.Now()
	if err := s.checkThrottle(name, now); err != nil {
		return domain.Session{}, "", domain.Human{}, err
	}
	// Always run the password check so an unknown name takes as long as a
	// known one.
	authErr := s.d.Auth.Authenticate(ctx, name, password)
	h, err := s.d.Humans.Human(ctx, name)
	if err == nil {
		err = authErr
	}
	if err != nil {
		s.failSignIn(ctx, name, remote, now, err)
		return domain.Session{}, "", domain.Human{}, fmt.Errorf("sign in %s: %w", name, core.ErrUnauthorized)
	}
	s.mu.Lock()
	s.throttle.Succeed(name)
	s.mu.Unlock()
	sess, secret, err := domain.NewSession(s.d.Rand, name, now)
	if err != nil {
		return domain.Session{}, "", domain.Human{}, err
	}
	if err := s.d.Sessions.SaveSession(ctx, sess); err != nil {
		return domain.Session{}, "", domain.Human{}, fmt.Errorf("save session: %w", err)
	}
	h.LastLoginAt = now
	if err := s.d.Humans.UpdateHuman(ctx, h); err != nil {
		return domain.Session{}, "", domain.Human{}, fmt.Errorf("record sign-in: %w", err)
	}
	s.publish(ctx, "agentsone.auth.login.succeeded", map[string]string{"user": name, "method": "password", "remote": remote})
	return sess, secret, h, nil
}

func (s *Service) checkThrottle(name string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if wait := s.throttle.RetryAfter(name, now); wait > 0 {
		return &TooManyAttempts{RetryAfter: wait}
	}
	return nil
}

func (s *Service) failSignIn(ctx context.Context, name, remote string, now time.Time, cause error) {
	s.mu.Lock()
	s.throttle.Fail(name, now)
	s.mu.Unlock()
	reason := "bad-credentials"
	if errors.Is(cause, core.ErrNotFound) {
		reason = "unknown-user"
	}
	user := name
	if len(user) > 32 {
		user = user[:32]
	}
	s.publish(ctx, "agentsone.auth.login.failed", map[string]string{"user": user, "method": "password", "remote": remote, "reason": reason})
}

// SessionCaller returns the human signed in with the cookie secret.
func (s *Service) SessionCaller(ctx context.Context, secret string) (core.Caller, error) {
	id := domain.SessionID(secret)
	sess, err := s.d.Sessions.Session(ctx, id)
	if errors.Is(err, core.ErrNotFound) {
		return core.Anonymous, fmt.Errorf("session: %w", core.ErrUnauthorized)
	}
	if err != nil {
		return core.Anonymous, fmt.Errorf("read session: %w", err)
	}
	if !sess.Live(s.d.Clock.Now()) {
		_ = s.d.Sessions.DeleteSession(ctx, id) // best effort; the session is refused either way
		return core.Anonymous, fmt.Errorf("session expired: %w", core.ErrUnauthorized)
	}
	h, err := s.d.Humans.Human(ctx, sess.Human)
	if err != nil {
		return core.Anonymous, fmt.Errorf("session human: %w", core.ErrUnauthorized)
	}
	return h.Caller(), nil
}

// SignOut ends the session with the cookie secret.
func (s *Service) SignOut(ctx context.Context, secret string) error {
	id := domain.SessionID(secret)
	sess, err := s.d.Sessions.Session(ctx, id)
	if err != nil {
		return fmt.Errorf("sign out: %w", core.ErrUnauthorized)
	}
	if err := s.d.Sessions.DeleteSession(ctx, id); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	s.publish(ctx, "agentsone.auth.logout", map[string]string{"user": sess.Human})
	return nil
}

// Issue makes a scoped token for subject (core.TokenIssuer).
func (s *Service) Issue(ctx context.Context, subject core.Caller, scopes []string, ttl time.Duration) (core.Token, error) {
	t, value, err := domain.NewToken(s.d.Rand, subject, scopes, s.d.Clock.Now(), ttl)
	if err != nil {
		return core.Token{}, fmt.Errorf("issue token: %w", err)
	}
	if err := s.d.Tokens.SaveToken(ctx, t); err != nil {
		return core.Token{}, fmt.Errorf("save token: %w", err)
	}
	return core.Token{Value: value, Expires: t.ExpiresAt}, nil
}

// Verify returns the caller of a token value (core.TokenIssuer).
func (s *Service) Verify(ctx context.Context, value string) (core.Caller, error) {
	id, secret, err := domain.ParseTokenValue(value)
	if err != nil {
		return core.Anonymous, err
	}
	t, err := s.d.Tokens.Token(ctx, id)
	if errors.Is(err, core.ErrNotFound) {
		return core.Anonymous, fmt.Errorf("token: %w", core.ErrUnauthorized)
	}
	if err != nil {
		return core.Anonymous, fmt.Errorf("read token: %w", err)
	}
	if err := t.Check(secret, s.d.Clock.Now()); err != nil {
		return core.Anonymous, err
	}
	return t.Caller(), nil
}

// RevokeToken ends a token. Humans revoke any token, others only their own.
func (s *Service) RevokeToken(ctx context.Context, by core.Caller, id string) error {
	t, err := s.d.Tokens.Token(ctx, id)
	if err != nil {
		return fmt.Errorf("revoke token %s: %w", id, err)
	}
	if !t.MayBeRevokedBy(by) {
		return fmt.Errorf("revoke token %s: %w", id, core.ErrForbidden)
	}
	if err := s.d.Tokens.RevokeToken(ctx, id, s.d.Clock.Now()); err != nil {
		return fmt.Errorf("revoke token %s: %w", id, err)
	}
	s.audit(ctx, by, "token.revoke", "token:"+id)
	return nil
}

// HumanByUID names the human account owning uid, for socket callers.
func (s *Service) HumanByUID(ctx context.Context, uid int) (string, bool) {
	h, err := s.d.Humans.HumanByUID(ctx, uid)
	if err != nil {
		return "", false
	}
	return h.Name, true
}

func (s *Service) publish(ctx context.Context, typ string, data map[string]string) {
	b, err := json.Marshal(data)
	if err != nil {
		return
	}
	// Events are a notification; a missing bus must not block sign-in.
	_, _ = s.d.Events.Publish(ctx, core.Event{
		SpecVersion: "1.0", Source: "agentsone/auth", Type: typ, DataContentType: "application/json",
		Data: b, Actor: "system:auth", ChartScope: "machine",
	})
}

func (s *Service) audit(ctx context.Context, by core.Caller, action, target string) {
	// The audit module may not be up yet in early boot; the action still happened.
	_ = s.d.Audit.Append(ctx, core.AuditEntry{Actor: string(by.Kind) + ":" + by.Name, Action: action, Target: target})
}
