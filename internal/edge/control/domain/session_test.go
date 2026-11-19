// SPDX-License-Identifier: MIT

package domain_test

import (
	"crypto/rand"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/edge/control/domain"
)

var t0 = time.Date(2026, 11, 19, 9, 0, 0, 0, time.UTC)

func newSessions(t *testing.T, limit int) *domain.Sessions {
	t.Helper()
	return domain.NewSessions(domain.SessionLimits{Idle: 15 * time.Minute, Absolute: 12 * time.Hour, Max: limit}, rand.Reader)
}

func start(t *testing.T, s *domain.Sessions, now time.Time) domain.Session {
	t.Helper()
	sess, err := s.Start(now)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	return sess
}

func TestSessions_StartGivesAnUnsignedSessionWithItsOwnTokens(t *testing.T) {
	t.Parallel()
	s := newSessions(t, 10)
	a, b := start(t, s, t0), start(t, s, t0)

	if a.SignedIn() {
		t.Fatal("a new session is signed in")
	}
	if len(a.ID) < 40 || len(a.CSRF) < 40 {
		t.Fatalf("id %q or csrf %q is too short to be unguessable", a.ID, a.CSRF)
	}
	if a.ID == b.ID || a.CSRF == b.CSRF || a.ID == a.CSRF {
		t.Fatal("two sessions share an id or a token")
	}
}

func TestSessions_StartFailsWithoutEntropy(t *testing.T) {
	t.Parallel()
	s := domain.NewSessions(domain.SessionLimits{Idle: time.Minute, Absolute: time.Hour, Max: 1}, entropy(0))
	if _, err := s.Start(t0); err == nil {
		t.Fatal("Start() with no entropy: error = nil, want an error")
	}
}

func TestSessions_GetEndsAnIdleSession(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		after time.Duration
		alive bool
	}{
		{"just inside the idle limit", 15*time.Minute - time.Second, true},
		{"on the idle limit", 15 * time.Minute, false},
		{"past the idle limit", 16 * time.Minute, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := newSessions(t, 10)
			sess := start(t, s, t0)

			_, ok := s.Get(sess.ID, t0.Add(tt.after))

			if ok != tt.alive {
				t.Fatalf("Get() after %v ok = %v, want %v", tt.after, ok, tt.alive)
			}
		})
	}
}

func TestSessions_GetKeepsAnActiveSessionAliveUntilTheAbsoluteLimit(t *testing.T) {
	t.Parallel()
	s := newSessions(t, 10)
	sess := start(t, s, t0)
	now := t0
	for now.Before(t0.Add(12*time.Hour - 10*time.Minute)) {
		now = now.Add(10 * time.Minute)
		if _, ok := s.Get(sess.ID, now); !ok {
			t.Fatalf("active session ended at %v, before the absolute limit", now.Sub(t0))
		}
	}

	if _, ok := s.Get(sess.ID, t0.Add(12*time.Hour)); ok {
		t.Fatal("session lived past the absolute limit")
	}
}

func TestSessions_GetRefusesAnUnknownID(t *testing.T) {
	t.Parallel()
	s := newSessions(t, 10)
	if _, ok := s.Get("nope", t0); ok {
		t.Fatal("Get() of an unknown id ok = true")
	}
}

func TestSessions_SignInReplacesTheSessionID(t *testing.T) {
	t.Parallel()
	s := newSessions(t, 10)
	before := start(t, s, t0)

	after, err := s.SignIn(before.ID, "tag-1", t0)
	if err != nil {
		t.Fatalf("SignIn() error = %v", err)
	}

	if !after.SignedIn() || after.Credential != "tag-1" {
		t.Fatalf("after sign in: %+v, want signed in with tag-1", after)
	}
	if after.ID == before.ID || after.CSRF == before.CSRF {
		t.Fatal("sign in kept the old id or token: session fixation")
	}
	if _, ok := s.Get(before.ID, t0); ok {
		t.Fatal("the old session id still works after sign in")
	}
	if _, ok := s.Get(after.ID, t0); !ok {
		t.Fatal("the new session id does not work")
	}
}

func TestSessions_SignInRefusesAnUnknownSession(t *testing.T) {
	t.Parallel()
	s := newSessions(t, 10)
	if _, err := s.SignIn("nope", "tag", t0); err == nil {
		t.Fatal("SignIn() of an unknown session: error = nil")
	}
}

func TestSessions_SignInFailsWithoutEntropy(t *testing.T) {
	t.Parallel()
	s := domain.NewSessions(domain.SessionLimits{Idle: time.Minute, Absolute: time.Hour, Max: 2}, entropy(16))
	sess := start(t, s, t0)
	if _, err := s.SignIn(sess.ID, "tag", t0); err == nil {
		t.Fatal("SignIn() with entropy used up: error = nil")
	}
}

func TestSessions_EndStopsTheSession(t *testing.T) {
	t.Parallel()
	s := newSessions(t, 10)
	sess := start(t, s, t0)

	s.End(sess.ID)

	if _, ok := s.Get(sess.ID, t0); ok {
		t.Fatal("ended session still works")
	}
}

func TestSessions_EndAllStopsEverySession(t *testing.T) {
	t.Parallel()
	s := newSessions(t, 10)
	a, b := start(t, s, t0), start(t, s, t0)

	s.EndAll()

	for _, id := range []string{a.ID, b.ID} {
		if _, ok := s.Get(id, t0); ok {
			t.Fatal("a session survived EndAll")
		}
	}
}

func TestSessions_StartDropsTheLeastRecentlySeenSessionAtTheLimit(t *testing.T) {
	t.Parallel()
	s := newSessions(t, 2)
	oldest := start(t, s, t0)
	kept := start(t, s, t0.Add(time.Second))
	s.Get(oldest.ID, t0.Add(2*time.Second)) // oldest is now the most recently seen

	start(t, s, t0.Add(3*time.Second))

	if _, ok := s.Get(oldest.ID, t0.Add(3*time.Second)); !ok {
		t.Fatal("the most recently seen session was dropped")
	}
	if _, ok := s.Get(kept.ID, t0.Add(3*time.Second)); ok {
		t.Fatal("the least recently seen session was kept past the limit")
	}
}

func TestSessions_AFloodOfVisitorsNeverDropsASignedInSession(t *testing.T) {
	t.Parallel()
	s := newSessions(t, 3)
	human, err := s.SignIn(start(t, s, t0).ID, "tag", t0)
	if err != nil {
		t.Fatal(err)
	}

	for i := range 50 {
		start(t, s, t0.Add(time.Duration(i+1)*time.Second))
	}

	if _, ok := s.Get(human.ID, t0.Add(time.Minute)); !ok {
		t.Fatal("anonymous visitors pushed out the signed-in session")
	}
}

func TestSessions_FlashIsReadOnce(t *testing.T) {
	t.Parallel()
	s := newSessions(t, 10)
	sess := start(t, s, t0)

	s.SetFlash(sess.ID, domain.Flash{Kind: domain.FlashDone, Text: "Saved."})

	if got := s.TakeFlash(sess.ID); got.Text != "Saved." || got.Kind != domain.FlashDone {
		t.Fatalf("first TakeFlash() = %+v, want the message", got)
	}
	if got := s.TakeFlash(sess.ID); got.Text != "" {
		t.Fatalf("second TakeFlash() = %+v, want empty", got)
	}
	s.SetFlash("nope", domain.Flash{Text: "lost"})
	if got := s.TakeFlash("nope"); got.Text != "" {
		t.Fatalf("flash of an unknown session = %+v, want empty", got)
	}
}

func TestSession_CSRFMatches(t *testing.T) {
	t.Parallel()
	s := newSessions(t, 10)
	sess := start(t, s, t0)

	if !sess.CSRFMatches(sess.CSRF) {
		t.Fatal("the session's own token was refused")
	}
	first := "x"
	if sess.CSRF[0] == 'x' {
		first = "y"
	}
	for _, bad := range []string{"", sess.CSRF[:len(sess.CSRF)-1], sess.CSRF + "x", first + sess.CSRF[1:]} {
		if sess.CSRFMatches(bad) {
			t.Fatalf("token %q was accepted", bad)
		}
	}
	var zero domain.Session
	if zero.CSRFMatches("") {
		t.Fatal("an empty token matched an empty session")
	}
}
