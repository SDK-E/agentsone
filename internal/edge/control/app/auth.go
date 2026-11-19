// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/SDK-E/agentsone/internal/edge/control/domain"
)

// Errors of the sign-in use cases.
var (
	ErrAlreadySetUp    = errors.New("a passphrase is already set")
	ErrNotSetUp        = errors.New("no passphrase is set yet")
	ErrWrongCode       = errors.New("this is not the setup code")
	ErrWrongPassphrase = errors.New("this is not the passphrase")
)

// Session timing and the sign-in limiter. After five failures each try waits twice as long as
// the one before, never more than a minute: with a passphrase of at least 12 characters, one
// guess a minute makes guessing hopeless, and the owner is never locked out for long.
const (
	IdleTimeout     = 15 * time.Minute
	AbsoluteTimeout = 12 * time.Hour
	MaxSessions     = 256
	FreeFailures    = 5
	FirstLock       = time.Second
	LongestLock     = time.Minute
)

// AuthOptions configure Auth.
type AuthOptions struct {
	Credentials Credentials
	Hasher      Hasher
	Clock       Clock
	// Entropy feeds setup codes, session ids and form tokens: crypto/rand in production.
	Entropy io.Reader
	Log     *slog.Logger
}

// Auth is the human-only sign-in of the control page. While no passphrase is set, it prints a
// one-time setup code to the log; whoever can read the log sets the passphrase with it. After
// that the passphrase signs in. A reset (run in the edge container) clears the passphrase, which
// signs everyone out and brings a new setup code.
type Auth struct {
	creds    Credentials
	hasher   Hasher
	clock    Clock
	entropy  io.Reader
	log      *slog.Logger
	sessions *domain.Sessions
	limiter  *domain.Limiter

	// mu guards the setup code and runs one passphrase check at a time, so a burst of tries
	// cannot use up the memory of the container (each hash takes tens of megabytes).
	mu   sync.Mutex
	code domain.SetupCode
}

// NewAuth returns the sign-in.
func NewAuth(o AuthOptions) *Auth {
	return &Auth{
		creds: o.Credentials, hasher: o.Hasher, clock: o.Clock, entropy: o.Entropy, log: o.Log,
		sessions: domain.NewSessions(domain.SessionLimits{Idle: IdleTimeout, Absolute: AbsoluteTimeout, Max: MaxSessions}, o.Entropy),
		limiter:  domain.NewLimiter(FreeFailures, FirstLock, LongestLock),
	}
}

// Visit is what a request knows about its visitor.
type Visit struct {
	Session    domain.Session
	NeedsSetup bool
	SignedIn   bool
}

// tag names a passphrase hash without revealing it. A session signed in under one hash stops
// working when the hash changes.
func tag(hash string) string {
	sum := sha256.Sum256([]byte(hash))
	return hex.EncodeToString(sum[:8])
}

// credential reads the stored hash; "" means none is set.
func (a *Auth) credential(ctx context.Context) (string, error) {
	hash, err := a.creds.Get(ctx)
	if errors.Is(err, ErrNoCredential) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return hash, nil
}

// Visit finds or starts the visitor's session. A session that timed out, or that signed in with
// a passphrase that has since been reset, is replaced by a new one that is not signed in. While
// no passphrase is set, it makes sure a setup code exists and is in the log.
func (a *Auth) Visit(ctx context.Context, sessionID string) (Visit, error) {
	hash, err := a.credential(ctx)
	if err != nil {
		return Visit{}, err
	}
	now := a.clock.Now()
	if hash == "" {
		if err := a.ensureCode(); err != nil {
			return Visit{}, err
		}
	}
	sess, ok := a.sessions.Get(sessionID, now)
	if ok && sess.SignedIn() && (hash == "" || sess.Credential != tag(hash)) {
		a.sessions.End(sess.ID)
		ok = false
	}
	if !ok {
		if sess, err = a.sessions.Start(now); err != nil {
			return Visit{}, err
		}
	}
	return Visit{Session: sess, NeedsSetup: hash == "", SignedIn: sess.SignedIn()}, nil
}

// Prepare runs at start: while no passphrase is set, it prints the setup code to the log, so the
// person who starts the stack finds it with `docker compose logs control`.
func (a *Auth) Prepare(ctx context.Context) error {
	hash, err := a.credential(ctx)
	if err != nil {
		return err
	}
	if hash == "" {
		return a.ensureCode()
	}
	return nil
}

// ensureCode makes a setup code and logs it, once per period without a passphrase.
func (a *Auth) ensureCode() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.code.String() != "" {
		return nil
	}
	code, err := domain.NewSetupCode(a.entropy)
	if err != nil {
		return err
	}
	a.code = code
	a.sessions.EndAll()
	a.log.Info("control page: no passphrase is set; open the control page and enter this setup code",
		"setup_code", code.String())
	return nil
}

func (a *Auth) wait() error {
	if w := a.limiter.Wait(a.clock.Now()); w > 0 {
		return &WaitError{Wait: w}
	}
	return nil
}

// Setup sets the first passphrase with the setup code from the log and signs the session in.
// The code works once. Wrong codes count towards the sign-in limiter.
func (a *Auth) Setup(ctx context.Context, sessionID, code, pass, confirm string) (domain.Session, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.wait(); err != nil {
		return domain.Session{}, err
	}
	hash, err := a.credential(ctx)
	if err != nil {
		return domain.Session{}, err
	}
	if hash != "" {
		return domain.Session{}, ErrAlreadySetUp
	}
	if !a.code.Matches(code) {
		a.limiter.Fail(a.clock.Now())
		a.log.Warn("control page: wrong setup code")
		return domain.Session{}, ErrWrongCode
	}
	if err := domain.CheckPassphrase(pass, confirm); err != nil {
		return domain.Session{}, err
	}
	hash, err = a.hasher.Hash(pass)
	if err != nil {
		return domain.Session{}, fmt.Errorf("hash passphrase: %w", err)
	}
	if err := a.creds.Create(ctx, hash); err != nil {
		if errors.Is(err, ErrCredentialSet) {
			return domain.Session{}, ErrAlreadySetUp
		}
		return domain.Session{}, err
	}
	a.code = domain.SetupCode{}
	a.limiter.Succeed()
	a.log.Info("control page: passphrase set")
	return a.sessions.SignIn(sessionID, tag(hash), a.clock.Now())
}

// Login signs the session in with the passphrase, under a new session id.
func (a *Auth) Login(ctx context.Context, sessionID, pass string) (domain.Session, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.wait(); err != nil {
		return domain.Session{}, err
	}
	hash, err := a.credential(ctx)
	if err != nil {
		return domain.Session{}, err
	}
	if hash == "" {
		return domain.Session{}, ErrNotSetUp
	}
	ok := false
	if len(pass) <= domain.MaxPassphraseBytes {
		if ok, err = a.hasher.Verify(hash, pass); err != nil {
			return domain.Session{}, fmt.Errorf("check passphrase: %w", err)
		}
	}
	if !ok {
		a.limiter.Fail(a.clock.Now())
		a.log.Warn("control page: sign-in failed")
		return domain.Session{}, ErrWrongPassphrase
	}
	a.limiter.Succeed()
	a.log.Info("control page: control sign-in")
	return a.sessions.SignIn(sessionID, tag(hash), a.clock.Now())
}

// Logout ends the session.
func (a *Auth) Logout(sessionID string) {
	a.sessions.End(sessionID)
	a.log.Info("control page: signed out")
}

// SetFlash sets the message the next page of this session shows.
func (a *Auth) SetFlash(sessionID string, f domain.Flash) { a.sessions.SetFlash(sessionID, f) }

// TakeFlash returns the message for this page and clears it.
func (a *Auth) TakeFlash(sessionID string) domain.Flash { return a.sessions.TakeFlash(sessionID) }
