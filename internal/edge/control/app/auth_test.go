// SPDX-License-Identifier: MIT

package app_test

import (
	"bytes"
	"crypto/rand"
	"errors"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/edge/control/app"
	"github.com/SDK-E/agentsone/internal/edge/control/controltest"
	"github.com/SDK-E/agentsone/internal/edge/control/domain"
)

var t0 = time.Date(2026, 11, 19, 9, 0, 0, 0, time.UTC)

const goodPass = "correct horse battery"

type authRig struct {
	auth  *app.Auth
	creds *controltest.Credentials
	clock *controltest.Clock
	log   *syncBuffer
}

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func newAuth(t *testing.T) authRig {
	t.Helper()
	rig := authRig{creds: &controltest.Credentials{}, clock: controltest.NewClock(t0), log: &syncBuffer{}}
	rig.auth = app.NewAuth(app.AuthOptions{
		Credentials: rig.creds, Hasher: controltest.Hasher{}, Clock: rig.clock, Entropy: rand.Reader,
		Log: slog.New(slog.NewTextHandler(rig.log, nil)),
	})
	return rig
}

var codeInLog = regexp.MustCompile(`setup_code=([A-Z0-9]{4}-[A-Z0-9]{4}-[A-Z0-9]{4})`)

func (r authRig) code(t *testing.T) string {
	t.Helper()
	m := codeInLog.FindAllStringSubmatch(r.log.String(), -1)
	if len(m) == 0 {
		t.Fatalf("no setup code in the log:\n%s", r.log.String())
	}
	return m[len(m)-1][1]
}

func (r authRig) visit(t *testing.T, sid string) app.Visit {
	t.Helper()
	v, err := r.auth.Visit(t.Context(), sid)
	if err != nil {
		t.Fatalf("Visit() error = %v", err)
	}
	return v
}

func (r authRig) setUp(t *testing.T) domain.Session {
	t.Helper()
	v := r.visit(t, "")
	sess, err := r.auth.Setup(t.Context(), v.Session.ID, r.code(t), goodPass, goodPass)
	if err != nil {
		t.Fatalf("Setup() error = %v", err)
	}
	return sess
}

func TestVisit_WithoutPassphraseAsksForSetupAndLogsTheCodeOnce(t *testing.T) {
	t.Parallel()
	r := newAuth(t)

	first := r.visit(t, "")
	second := r.visit(t, first.Session.ID)

	if !first.NeedsSetup || first.SignedIn {
		t.Fatalf("first visit = %+v, want needs setup, not signed in", first)
	}
	if !second.NeedsSetup || second.Session.ID != first.Session.ID {
		t.Fatalf("second visit = %+v, want the same session, still needs setup", second)
	}
	if n := len(codeInLog.FindAllString(r.log.String(), -1)); n != 1 {
		t.Fatalf("setup code logged %d times, want once:\n%s", n, r.log.String())
	}
}

func TestPrepare_LogsTheSetupCodeAtStartWhenNoPassphraseIsSet(t *testing.T) {
	t.Parallel()
	r := newAuth(t)

	if err := r.auth.Prepare(t.Context()); err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}

	r.code(t)
}

func TestPrepare_LogsNoCodeWhenAPassphraseIsSet(t *testing.T) {
	t.Parallel()
	r := newAuth(t)
	if err := r.creds.Create(t.Context(), "fake$x"); err != nil {
		t.Fatal(err)
	}

	if err := r.auth.Prepare(t.Context()); err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}

	if codeInLog.MatchString(r.log.String()) {
		t.Fatal("a setup code was logged while a passphrase is set")
	}
}

func TestPrepare_ReportsAnEdgeThatDoesNotAnswer(t *testing.T) {
	t.Parallel()
	r := newAuth(t)
	r.creds.Err = controltest.ErrDown

	if err := r.auth.Prepare(t.Context()); !errors.Is(err, app.ErrEdgeUnavailable) {
		t.Fatalf("Prepare() error = %v, want %v", err, app.ErrEdgeUnavailable)
	}
}

func TestVisit_StartsANewSessionForAnUnknownID(t *testing.T) {
	t.Parallel()
	r := newAuth(t)

	v := r.visit(t, "made-up")

	if v.Session.ID == "made-up" || v.Session.ID == "" {
		t.Fatalf("session id = %q, want a fresh one", v.Session.ID)
	}
}

func TestVisit_ReportsAnEdgeThatDoesNotAnswer(t *testing.T) {
	t.Parallel()
	r := newAuth(t)
	r.creds.Err = controltest.ErrDown

	if _, err := r.auth.Visit(t.Context(), ""); !errors.Is(err, app.ErrEdgeUnavailable) {
		t.Fatalf("Visit() error = %v, want %v", err, app.ErrEdgeUnavailable)
	}
}

func TestSetup_WithTheCodeSetsThePassphraseAndSignsIn(t *testing.T) {
	t.Parallel()
	r := newAuth(t)

	sess := r.setUp(t)

	if r.creds.Hash() != "fake$"+goodPass {
		t.Fatalf("stored hash = %q, want the hash of the passphrase", r.creds.Hash())
	}
	v := r.visit(t, sess.ID)
	if !v.SignedIn || v.NeedsSetup {
		t.Fatalf("visit after setup = %+v, want signed in", v)
	}
}

func TestSetup_CodeWorksOnlyOnce(t *testing.T) {
	t.Parallel()
	r := newAuth(t)
	code := func() string { r.visit(t, ""); return r.code(t) }()
	v := r.visit(t, "")
	if _, err := r.auth.Setup(t.Context(), v.Session.ID, code, goodPass, goodPass); err != nil {
		t.Fatalf("first Setup() error = %v", err)
	}

	_, err := r.auth.Setup(t.Context(), v.Session.ID, code, "another passphrase", "another passphrase")

	if !errors.Is(err, app.ErrAlreadySetUp) {
		t.Fatalf("second Setup() error = %v, want %v", err, app.ErrAlreadySetUp)
	}
	if r.creds.Hash() != "fake$"+goodPass {
		t.Fatal("the second setup changed the passphrase")
	}
}

func TestSetup_RefusesAWrongCodeAndStoresNothing(t *testing.T) {
	t.Parallel()
	r := newAuth(t)
	v := r.visit(t, "")

	_, err := r.auth.Setup(t.Context(), v.Session.ID, "AAAA-AAAA-AAAA", goodPass, goodPass)

	if !errors.Is(err, app.ErrWrongCode) {
		t.Fatalf("Setup() error = %v, want %v", err, app.ErrWrongCode)
	}
	if r.creds.Hash() != "" {
		t.Fatal("a wrong code stored a passphrase")
	}
}

func TestSetup_RefusesAWeakPassphraseAndKeepsTheCode(t *testing.T) {
	t.Parallel()
	r := newAuth(t)
	v := r.visit(t, "")
	code := r.code(t)

	_, err := r.auth.Setup(t.Context(), v.Session.ID, code, "short", "short")

	if !errors.Is(err, domain.ErrPassphraseTooShort) {
		t.Fatalf("Setup() error = %v, want %v", err, domain.ErrPassphraseTooShort)
	}
	if _, err := r.auth.Setup(t.Context(), v.Session.ID, code, goodPass, goodPass); err != nil {
		t.Fatalf("Setup() with the same code after a weak passphrase: error = %v", err)
	}
}

func TestSetup_WhenSomeoneElseSetItFirstRefuses(t *testing.T) {
	t.Parallel()
	r := newAuth(t)
	v := r.visit(t, "")
	code := r.code(t)
	if err := r.creds.Create(t.Context(), "fake$theirs"); err != nil {
		t.Fatal(err)
	}

	if _, err := r.auth.Setup(t.Context(), v.Session.ID, code, goodPass, goodPass); !errors.Is(err, app.ErrAlreadySetUp) {
		t.Fatalf("Setup() error = %v, want %v", err, app.ErrAlreadySetUp)
	}
}

func TestSetup_SlowsDownAfterRepeatedWrongCodes(t *testing.T) {
	t.Parallel()
	r := newAuth(t)
	v := r.visit(t, "")
	for range 5 {
		_, _ = r.auth.Setup(t.Context(), v.Session.ID, "AAAA-AAAA-AAAA", goodPass, goodPass)
	}
	_, _ = r.auth.Setup(t.Context(), v.Session.ID, "AAAA-AAAA-AAAA", goodPass, goodPass)

	_, err := r.auth.Setup(t.Context(), v.Session.ID, r.code(t), goodPass, goodPass)

	var wait *app.WaitError
	if !errors.As(err, &wait) || wait.Wait <= 0 {
		t.Fatalf("Setup() after six wrong codes: error = %v, want a WaitError", err)
	}
	if r.creds.Hash() != "" {
		t.Fatal("a locked setup stored a passphrase")
	}
}

func TestLogin_WithThePassphraseSignsInUnderANewSessionID(t *testing.T) {
	t.Parallel()
	r := newAuth(t)
	r.setUp(t)
	anon := r.visit(t, "")

	sess, err := r.auth.Login(t.Context(), anon.Session.ID, goodPass)
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}

	if sess.ID == anon.Session.ID {
		t.Fatal("login kept the session id")
	}
	if v := r.visit(t, sess.ID); !v.SignedIn {
		t.Fatalf("visit after login = %+v, want signed in", v)
	}
	if !strings.Contains(r.log.String(), "control sign-in") {
		t.Fatalf("no sign-in line in the log:\n%s", r.log.String())
	}
}

func TestLogin_RefusesAWrongPassphrase(t *testing.T) {
	t.Parallel()
	r := newAuth(t)
	r.setUp(t)
	anon := r.visit(t, "")

	_, err := r.auth.Login(t.Context(), anon.Session.ID, "wrong passphrase here")

	if !errors.Is(err, app.ErrWrongPassphrase) {
		t.Fatalf("Login() error = %v, want %v", err, app.ErrWrongPassphrase)
	}
	if v := r.visit(t, anon.Session.ID); v.SignedIn {
		t.Fatal("a wrong passphrase signed in")
	}
	if strings.Contains(r.log.String(), "wrong passphrase here") {
		t.Fatal("the log holds the tried passphrase")
	}
}

func TestLogin_BeforeSetupRefuses(t *testing.T) {
	t.Parallel()
	r := newAuth(t)
	anon := r.visit(t, "")

	if _, err := r.auth.Login(t.Context(), anon.Session.ID, goodPass); !errors.Is(err, app.ErrNotSetUp) {
		t.Fatalf("Login() error = %v, want %v", err, app.ErrNotSetUp)
	}
}

func TestLogin_LocksAfterRepeatedFailuresAndOpensAfterTheWait(t *testing.T) {
	t.Parallel()
	r := newAuth(t)
	r.setUp(t)
	anon := r.visit(t, "")
	for range 6 {
		_, _ = r.auth.Login(t.Context(), anon.Session.ID, "wrong passphrase here")
	}

	_, err := r.auth.Login(t.Context(), anon.Session.ID, goodPass)
	var wait *app.WaitError
	if !errors.As(err, &wait) || wait.Wait != time.Second {
		t.Fatalf("Login() while locked: error = %v, want a 1s WaitError", err)
	}

	r.clock.Advance(time.Second)
	if _, err := r.auth.Login(t.Context(), anon.Session.ID, goodPass); err != nil {
		t.Fatalf("Login() after the wait: error = %v", err)
	}
}

func TestLogin_NeverLocksTheOwnerOutForMoreThanAMinute(t *testing.T) {
	t.Parallel()
	r := newAuth(t)
	r.setUp(t)
	anon := r.visit(t, "")
	for range 100 {
		r.clock.Advance(time.Hour) // past any lock, so each try counts
		_, _ = r.auth.Login(t.Context(), anon.Session.ID, "wrong passphrase here")
	}

	_, err := r.auth.Login(t.Context(), anon.Session.ID, goodPass)
	var wait *app.WaitError
	if !errors.As(err, &wait) || wait.Wait != time.Minute {
		t.Fatalf("Login() after 100 failures: error = %v, want a WaitError of exactly one minute", err)
	}

	r.clock.Advance(time.Minute)
	fresh := r.visit(t, "")
	if _, err := r.auth.Login(t.Context(), fresh.Session.ID, goodPass); err != nil {
		t.Fatalf("Login() one minute later: error = %v", err)
	}
}

func TestLogin_RefusesAnOverlongPassphraseWithoutHashingIt(t *testing.T) {
	t.Parallel()
	r := newAuth(t)
	r.setUp(t)
	anon := r.visit(t, "")
	long := strings.Repeat("a", domain.MaxPassphraseBytes+1)

	if _, err := r.auth.Login(t.Context(), anon.Session.ID, long); !errors.Is(err, app.ErrWrongPassphrase) {
		t.Fatalf("Login() error = %v, want %v", err, app.ErrWrongPassphrase)
	}
}

func TestVisit_AfterAResetSignsOutAndAsksForANewCode(t *testing.T) {
	t.Parallel()
	r := newAuth(t)
	sess := r.setUp(t)
	oldCode := r.code(t)

	r.creds.Reset()
	v := r.visit(t, sess.ID)

	if v.SignedIn || !v.NeedsSetup {
		t.Fatalf("visit after reset = %+v, want signed out, needs setup", v)
	}
	if r.code(t) == oldCode {
		t.Fatal("the used setup code came back after a reset")
	}
}

func TestVisit_AfterThePassphraseChangedElsewhereSignsOut(t *testing.T) {
	t.Parallel()
	r := newAuth(t)
	sess := r.setUp(t)
	r.creds.Reset()
	if err := r.creds.Create(t.Context(), "fake$someone else's"); err != nil {
		t.Fatal(err)
	}

	if v := r.visit(t, sess.ID); v.SignedIn {
		t.Fatal("a session from the old passphrase is still signed in")
	}
}

func TestVisit_EndsAnIdleSession(t *testing.T) {
	t.Parallel()
	r := newAuth(t)
	sess := r.setUp(t)

	r.clock.Advance(app.IdleTimeout)

	if v := r.visit(t, sess.ID); v.SignedIn || v.Session.ID == sess.ID {
		t.Fatalf("visit after the idle timeout = %+v, want a new unsigned session", v)
	}
}

func TestLogout_EndsTheSession(t *testing.T) {
	t.Parallel()
	r := newAuth(t)
	sess := r.setUp(t)

	r.auth.Logout(sess.ID)

	if v := r.visit(t, sess.ID); v.SignedIn {
		t.Fatal("still signed in after sign out")
	}
}

func TestFlash_IsShownOnce(t *testing.T) {
	t.Parallel()
	r := newAuth(t)
	sess := r.setUp(t)

	r.auth.SetFlash(sess.ID, domain.Flash{Kind: domain.FlashDone, Text: "Saved."})

	if got := r.auth.TakeFlash(sess.ID); got.Text != "Saved." {
		t.Fatalf("TakeFlash() = %+v", got)
	}
	if got := r.auth.TakeFlash(sess.ID); got.Text != "" {
		t.Fatalf("second TakeFlash() = %+v, want empty", got)
	}
}
