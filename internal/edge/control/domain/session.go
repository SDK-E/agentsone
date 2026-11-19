// SPDX-License-Identifier: MIT

package domain

import (
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"
)

// ErrNoSession is returned for a session id that is unknown or has ended.
var ErrNoSession = errors.New("no such session")

// SessionLimits bound sessions.
type SessionLimits struct {
	// Idle ends a session this long after its last request.
	Idle time.Duration
	// Absolute ends a session this long after it started, however active it is.
	Absolute time.Duration
	// Max is the most sessions held at once; the least recently seen goes first.
	Max int
}

// FlashKind is the kind of a one-shot message.
type FlashKind string

// Flash kinds.
const (
	FlashDone  FlashKind = "done"
	FlashError FlashKind = "error"
)

// Flash is a one-shot message shown on the next page, so a result never travels in a URL.
type Flash struct {
	Kind FlashKind
	Text string
	// Where names the part of the page the message belongs to; empty is the top of the page.
	Where string
}

// Session is one visitor's session. Every visitor has one, signed in or not, so every form can
// carry a token.
type Session struct {
	// ID is the cookie value.
	ID string
	// CSRF is the token every form of this session carries.
	CSRF string
	// Credential tags the passphrase the session signed in with; empty when not signed in. A
	// reset changes the tag, which ends the session.
	Credential string
	Created    time.Time
	LastSeen   time.Time
	flash      Flash
}

// SignedIn tells whether the session is signed in.
func (s Session) SignedIn() bool { return s.Credential != "" }

// CSRFMatches tells whether token is the session's form token, in constant time.
func (s Session) CSRFMatches(token string) bool {
	if s.CSRF == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(token), []byte(s.CSRF)) == 1
}

// tokenBytes is the size of session ids and form tokens: 256 bits.
const tokenBytes = 32

// Sessions holds the sessions in memory. The control service has no disk of its own, so a
// restart signs everyone out. It is safe for concurrent use.
type Sessions struct {
	limits  SessionLimits
	entropy io.Reader

	mu   sync.Mutex
	byID map[string]*Session
}

// NewSessions returns an empty store that draws ids and tokens from entropy.
func NewSessions(limits SessionLimits, entropy io.Reader) *Sessions {
	if limits.Max < 1 {
		limits.Max = 1
	}
	return &Sessions{limits: limits, entropy: entropy, byID: map[string]*Session{}}
}

func (s *Sessions) token() (string, error) {
	b := make([]byte, tokenBytes)
	if _, err := io.ReadFull(s.entropy, b); err != nil {
		return "", fmt.Errorf("draw session token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func (s *Sessions) newSession(now time.Time) (*Session, error) {
	id, err := s.token()
	if err != nil {
		return nil, err
	}
	csrf, err := s.token()
	if err != nil {
		return nil, err
	}
	return &Session{ID: id, CSRF: csrf, Created: now, LastSeen: now}, nil
}

// Start starts a session that is not signed in.
func (s *Sessions) Start(now time.Time) (Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, err := s.newSession(now)
	if err != nil {
		return Session{}, err
	}
	s.add(sess)
	return *sess, nil
}

// add stores sess, dropping the least recently seen sessions over the limit. Sessions that are
// not signed in go first, so a flood of visitors cannot push the human out. Callers hold mu.
func (s *Sessions) add(sess *Session) {
	for len(s.byID) >= s.limits.Max {
		var oldest *Session
		for _, cand := range s.byID {
			switch {
			case oldest == nil,
				oldest.SignedIn() && !cand.SignedIn(),
				oldest.SignedIn() == cand.SignedIn() && cand.LastSeen.Before(oldest.LastSeen):
				oldest = cand
			}
		}
		delete(s.byID, oldest.ID)
	}
	s.byID[sess.ID] = sess
}

// live returns the session if it has not timed out, and ends it if it has. Callers hold mu.
func (s *Sessions) live(id string, now time.Time) (*Session, bool) {
	sess, ok := s.byID[id]
	if !ok {
		return nil, false
	}
	if now.Sub(sess.LastSeen) >= s.limits.Idle || now.Sub(sess.Created) >= s.limits.Absolute {
		delete(s.byID, id)
		return nil, false
	}
	return sess, true
}

// Get returns a live session and marks it seen at now. A session past its idle or absolute
// limit is ended.
func (s *Sessions) Get(id string, now time.Time) (Session, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.live(id, now)
	if !ok {
		return Session{}, false
	}
	sess.LastSeen = now
	return *sess, true
}

// SignIn signs a session in. The session gets a new id and a new form token, and the old id
// stops working, so an id planted before sign-in is worth nothing after it.
func (s *Sessions) SignIn(id, credential string, now time.Time) (Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.live(id, now); !ok {
		return Session{}, ErrNoSession
	}
	sess, err := s.newSession(now)
	if err != nil {
		return Session{}, err
	}
	delete(s.byID, id)
	sess.Credential = credential
	s.add(sess)
	return *sess, nil
}

// End ends one session.
func (s *Sessions) End(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.byID, id)
}

// EndAll ends every session.
func (s *Sessions) EndAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	clear(s.byID)
}

// SetFlash sets the session's one-shot message. An unknown session is ignored.
func (s *Sessions) SetFlash(id string, f Flash) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sess, ok := s.byID[id]; ok {
		sess.flash = f
	}
}

// TakeFlash returns the session's one-shot message and clears it.
func (s *Sessions) TakeFlash(id string) Flash {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.byID[id]
	if !ok {
		return Flash{}
	}
	f := sess.flash
	sess.flash = Flash{}
	return f
}
