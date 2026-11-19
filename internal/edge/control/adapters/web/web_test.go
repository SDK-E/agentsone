// SPDX-License-Identifier: MIT

package web_test

import (
	"bytes"
	"crypto/rand"
	"crypto/tls"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/edge/control/adapters/web"
	"github.com/SDK-E/agentsone/internal/edge/control/app"
	"github.com/SDK-E/agentsone/internal/edge/control/controltest"
)

var t0 = time.Date(2026, 11, 19, 9, 0, 0, 0, time.UTC)

const pass = "correct horse battery"

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

type rig struct {
	h     http.Handler
	edge  *controltest.Edge
	creds *controltest.Credentials
	clock *controltest.Clock
	log   *syncBuffer
}

func newRig(t *testing.T, trusted ...netip.Prefix) *rig {
	t.Helper()
	r := &rig{creds: &controltest.Credentials{}, clock: controltest.NewClock(t0), log: &syncBuffer{}}
	log := slog.New(slog.NewTextHandler(r.log, &slog.HandlerOptions{Level: slog.LevelDebug}))
	r.edge = controltest.NewEdge(r.clock.Now, app.Provider{ID: "openai", KeyRequired: true})
	auth := app.NewAuth(app.AuthOptions{Credentials: r.creds, Hasher: controltest.Hasher{}, Clock: r.clock, Entropy: rand.Reader, Log: log})
	powers := app.NewPowers(app.PowersOptions{Network: r.edge, Models: r.edge, Money: r.edge, Freeze: r.edge})
	r.h = web.New(web.Options{Auth: auth, Powers: powers, Log: log, TrustedProxies: trusted})
	return r
}

// browser keeps the session cookie and the last form token, like a browser on the page.
type browser struct {
	t      *testing.T
	r      *rig
	cookie *http.Cookie
	csrf   string
	remote string
	tls    bool
	header http.Header
}

func (r *rig) browser(t *testing.T) *browser {
	return &browser{t: t, r: r, remote: "192.0.2.10:5555", header: http.Header{}}
}

var csrfField = regexp.MustCompile(`name="csrf" value="([^"]+)"`)

func (b *browser) do(method, target string, form url.Values) *httptest.ResponseRecorder {
	b.t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req := httptest.NewRequestWithContext(b.t.Context(), method, target, body)
	req.RemoteAddr = b.remote
	if b.tls {
		req.TLS = &tls.ConnectionState{}
	}
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for k, v := range b.header {
		req.Header[k] = v
	}
	if b.cookie != nil {
		req.AddCookie(b.cookie)
	}
	rec := httptest.NewRecorder()
	b.r.h.ServeHTTP(rec, req)
	for _, c := range rec.Result().Cookies() {
		if strings.Contains(c.Name, "agentsone_control") {
			// #nosec G124 -- the test browser's cookie jar: only name and value go back.
			b.cookie = &http.Cookie{Name: c.Name, Value: c.Value}
		}
	}
	if m := csrfField.FindStringSubmatch(rec.Body.String()); m != nil {
		b.csrf = m[1]
	}
	return rec
}

func (b *browser) get(target string) *httptest.ResponseRecorder {
	return b.do(http.MethodGet, target, nil)
}

func (b *browser) post(target string, form url.Values) *httptest.ResponseRecorder {
	b.t.Helper()
	if form == nil {
		form = url.Values{}
	}
	if !form.Has("csrf") {
		form.Set("csrf", b.csrf)
	}
	return b.do(http.MethodPost, target, form)
}

var codeInLog = regexp.MustCompile(`setup_code=([A-Z0-9-]{14})`)

func (r *rig) code(t *testing.T) string {
	t.Helper()
	m := codeInLog.FindAllStringSubmatch(r.log.String(), -1)
	if m == nil {
		t.Fatalf("no setup code in the log:\n%s", r.log.String())
	}
	return m[len(m)-1][1]
}

// signedIn returns a browser that went through setup.
func (r *rig) signedIn(t *testing.T) *browser {
	t.Helper()
	b := r.browser(t)
	b.get("/setup")
	rec := b.post("/setup", url.Values{"code": {r.code(t)}, "passphrase": {pass}, "confirm": {pass}})
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/" {
		t.Fatalf("setup = %d %s", rec.Code, rec.Body)
	}
	b.get("/")
	return b
}

func wantRedirect(t *testing.T, rec *httptest.ResponseRecorder, to string) {
	t.Helper()
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != to {
		t.Fatalf("answer = %d to %q, want 303 to %q\n%s", rec.Code, rec.Header().Get("Location"), to, rec.Body)
	}
}

func TestHome_WithoutPassphraseSendsToSetup(t *testing.T) {
	t.Parallel()
	b := newRig(t).browser(t)
	wantRedirect(t, b.get("/"), "/setup")
}

func TestSetup_ShowsTheFormAndPrintsTheCodeOnlyToTheLog(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	b := r.browser(t)

	rec := b.get("/setup")

	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "docker compose logs control") {
		t.Fatalf("GET /setup = %d\n%s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), r.code(t)) {
		t.Fatal("the setup page shows the setup code")
	}
}

func TestSetup_WithTheCodeSignsInAndOpensThePowers(t *testing.T) {
	t.Parallel()
	b := newRig(t).signedIn(t)

	rec := b.get("/")

	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Freeze all now") {
		t.Fatalf("GET / = %d\n%s", rec.Code, rec.Body)
	}
}

func TestSetup_TheCodeWorksOnce(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.signedIn(t)
	code := r.code(t)
	other := r.browser(t)
	other.get("/sign-in")

	rec := other.post("/setup", url.Values{"code": {code}, "passphrase": {"another passphrase"}, "confirm": {"another passphrase"}})

	wantRedirect(t, rec, "/sign-in")
	if r.creds.Hash() != "fake$"+pass {
		t.Fatal("the code set a second passphrase")
	}
}

func TestSetup_AWrongCodeShowsTheFormAgainWithoutThePassphrase(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	b := r.browser(t)
	b.get("/setup")

	rec := b.post("/setup", url.Values{"code": {"AAAA-AAAA-AAAA"}, "passphrase": {pass}, "confirm": {pass}})

	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "not the setup code") {
		t.Fatalf("POST /setup = %d\n%s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), pass) {
		t.Fatal("the page echoes the passphrase")
	}
}

func TestSetup_RefusesAShortPassphrase(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	b := r.browser(t)
	b.get("/setup")

	rec := b.post("/setup", url.Values{"code": {r.code(t)}, "passphrase": {"short"}, "confirm": {"short"}})

	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "at least 12 characters") {
		t.Fatalf("POST /setup = %d\n%s", rec.Code, rec.Body)
	}
}

func TestPost_WithoutTheFormTokenIsRefused(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	b := r.signedIn(t)

	for _, token := range []string{"", "forged", b.csrf + "x"} {
		rec := b.post("/freeze", url.Values{"csrf": {token}})
		if rec.Code != http.StatusForbidden {
			t.Fatalf("POST /freeze with token %q = %d, want 403", token, rec.Code)
		}
	}
	if s, _ := r.edge.Status(t.Context()); s.Frozen {
		t.Fatal("a forged form froze the stack")
	}
}

func TestPost_FromAnotherSiteIsRefused(t *testing.T) {
	t.Parallel()
	tests := []struct{ header, value string }{
		{"Origin", "https://evil.example"},
		{"Sec-Fetch-Site", "cross-site"},
		{"Sec-Fetch-Site", "same-site"},
	}
	for _, tt := range tests {
		t.Run(tt.header+" "+tt.value, func(t *testing.T) {
			t.Parallel()
			r := newRig(t)
			b := r.signedIn(t)
			b.header.Set(tt.header, tt.value)

			if rec := b.post("/freeze", nil); rec.Code != http.StatusForbidden {
				t.Fatalf("POST /freeze = %d, want 403", rec.Code)
			}
			if s, _ := r.edge.Status(t.Context()); s.Frozen {
				t.Fatal("a cross-site form froze the stack")
			}
		})
	}
}

func TestPost_WithAnOpaqueOriginIsAcceptedOnlyWhenTheBrowserSaysSameOrigin(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, fetchSite string
		want            int
	}{
		{"browser says same origin", "same-origin", http.StatusSeeOther},
		{"browser says nothing", "", http.StatusForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			b := newRig(t).signedIn(t)
			b.header.Set("Origin", "null")
			if tt.fetchSite != "" {
				b.header.Set("Sec-Fetch-Site", tt.fetchSite)
			}

			if rec := b.post("/freeze", nil); rec.Code != tt.want {
				t.Fatalf("POST /freeze = %d, want %d", rec.Code, tt.want)
			}
		})
	}
}

func TestPost_FromThePageItselfIsAccepted(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	b := r.signedIn(t)
	b.header.Set("Origin", "http://example.com")
	b.header.Set("Sec-Fetch-Site", "same-origin")

	wantRedirect(t, b.post("/freeze", nil), "/#freeze")
	if s, _ := r.edge.Status(t.Context()); !s.Frozen {
		t.Fatal("freeze did not reach the edge")
	}
}

func TestCookie_IsHttpOnlyStrictAndNotSecureOverPlainHTTP(t *testing.T) {
	t.Parallel()
	b := newRig(t).browser(t)

	rec := b.get("/setup")

	c := sessionCookie(t, rec)
	if c.Name != "agentsone_control" || !c.HttpOnly || c.SameSite != http.SameSiteStrictMode || c.Secure || c.Path != "/" {
		t.Fatalf("cookie = %+v, want agentsone_control, HttpOnly, SameSite=Strict, Path=/", c)
	}
	if rec.Header().Get("Strict-Transport-Security") != "" {
		t.Fatal("HSTS over plain HTTP")
	}
}

func sessionCookie(t *testing.T, rec *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, c := range rec.Result().Cookies() {
		if strings.Contains(c.Name, "agentsone_control") {
			return c
		}
	}
	t.Fatalf("no session cookie in %v", rec.Header()["Set-Cookie"])
	return nil
}

func TestCookie_IsSecureOverTLS(t *testing.T) {
	t.Parallel()
	b := newRig(t).browser(t)
	b.tls = true

	rec := b.get("/setup")

	c := sessionCookie(t, rec)
	if c.Name != "__Host-agentsone_control" || !c.Secure || !c.HttpOnly || c.SameSite != http.SameSiteStrictMode {
		t.Fatalf("cookie = %+v, want __Host- prefix and Secure", c)
	}
	if !strings.HasPrefix(rec.Header().Get("Strict-Transport-Security"), "max-age=") {
		t.Fatal("no HSTS over TLS")
	}
}

func TestCookie_TrustsForwardedProtoOnlyFromATrustedProxy(t *testing.T) {
	t.Parallel()
	proxy := netip.MustParsePrefix("10.77.42.32/27")

	untrusted := newRig(t).browser(t)
	untrusted.header.Set("X-Forwarded-Proto", "https")
	if c := sessionCookie(t, untrusted.get("/setup")); c.Secure {
		t.Fatal("X-Forwarded-Proto from anyone made the cookie Secure")
	}

	trusted := newRig(t, proxy).browser(t)
	trusted.remote = "10.77.42.40:443"
	trusted.header.Set("X-Forwarded-Proto", "https")
	if c := sessionCookie(t, trusted.get("/setup")); !c.Secure {
		t.Fatal("X-Forwarded-Proto from the trusted proxy did not make the cookie Secure")
	}
}

func TestPages_CarryStrictSecurityHeaders(t *testing.T) {
	t.Parallel()
	rec := newRig(t).browser(t).get("/setup")

	want := map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"Referrer-Policy":        "same-origin",
		"Cache-Control":          "no-store",
	}
	for k, v := range want {
		if got := rec.Header().Get(k); got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}
	csp := rec.Header().Get("Content-Security-Policy")
	for _, part := range []string{"default-src 'none'", "frame-ancestors 'none'", "form-action 'self'"} {
		if !strings.Contains(csp, part) {
			t.Errorf("CSP %q lacks %q", csp, part)
		}
	}
	if strings.Contains(csp, "unsafe-inline") || strings.Contains(csp, "script-src") {
		t.Errorf("CSP %q allows scripts", csp)
	}
}

func TestSignIn_WithThePassphraseOpensThePowers(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.signedIn(t)
	b := r.browser(t)
	wantRedirect(t, b.get("/"), "/sign-in")
	b.get("/sign-in")
	before := b.cookie.Value

	wantRedirect(t, b.post("/sign-in", url.Values{"passphrase": {pass}}), "/")

	if b.cookie.Value == before {
		t.Fatal("sign in kept the session id")
	}
	if rec := b.get("/"); rec.Code != http.StatusOK {
		t.Fatalf("GET / after sign in = %d", rec.Code)
	}
}

func TestSignIn_AWrongPassphraseIsRefusedWithoutEcho(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.signedIn(t)
	b := r.browser(t)
	b.get("/sign-in")

	rec := b.post("/sign-in", url.Values{"passphrase": {"wrong passphrase guess"}})

	if rec.Code != http.StatusUnauthorized || strings.Contains(rec.Body.String(), "wrong passphrase guess") {
		t.Fatalf("POST /sign-in = %d\n%s", rec.Code, rec.Body)
	}
	if strings.Contains(r.log.String(), "wrong passphrase guess") {
		t.Fatal("the log holds the tried passphrase")
	}
}

func TestSignIn_LocksAfterRepeatedFailuresWithRetryAfter(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.signedIn(t)
	b := r.browser(t)
	b.get("/sign-in")
	for range 6 {
		b.post("/sign-in", url.Values{"passphrase": {"wrong passphrase guess"}})
	}

	rec := b.post("/sign-in", url.Values{"passphrase": {pass}})

	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") != "1" {
		t.Fatalf("POST /sign-in while locked = %d, Retry-After %q", rec.Code, rec.Header().Get("Retry-After"))
	}
	if !strings.Contains(rec.Body.String(), "Too many wrong tries") {
		t.Fatalf("body does not explain the wait:\n%s", rec.Body)
	}
	r.clock.Advance(time.Second)
	wantRedirect(t, b.post("/sign-in", url.Values{"passphrase": {pass}}), "/")
}

func TestSignOut_EndsTheSession(t *testing.T) {
	t.Parallel()
	b := newRig(t).signedIn(t)

	wantRedirect(t, b.post("/sign-out", nil), "/sign-in")
	wantRedirect(t, b.get("/"), "/sign-in")
}

func TestPowers_NeedASignedInSession(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.signedIn(t)
	b := r.browser(t)
	b.get("/sign-in")

	wantRedirect(t, b.post("/freeze", nil), "/sign-in")
	if s, _ := r.edge.Status(t.Context()); s.Frozen {
		t.Fatal("a visitor who is not signed in froze the stack")
	}
}

func TestSetKey_NeverEchoesTheKey(t *testing.T) {
	t.Parallel()
	const key = "sk-test-NEVER-SHOW-ME-123"
	r := newRig(t)
	b := r.signedIn(t)

	rec := b.post("/providers/key", url.Values{"provider": {"openai"}, "key": {key}})
	wantRedirect(t, rec, "/#models")
	page := b.get("/")

	if r.edge.Key("openai") != key {
		t.Fatal("the key did not reach the edge")
	}
	for name, s := range map[string]string{
		"redirect": rec.Body.String() + rec.Header().Get("Location"), "page": page.Body.String(), "log": r.log.String(),
	} {
		if strings.Contains(s, key) || strings.Contains(s, "NEVER-SHOW") {
			t.Fatalf("the %s holds the key", name)
		}
	}
	if !strings.Contains(page.Body.String(), controltest.Fingerprint(key)) {
		t.Fatal("the page does not show the key's fingerprint")
	}
}

func TestSetKey_ErrorNeverEchoesTheKey(t *testing.T) {
	t.Parallel()
	const key = "sk-test-NEVER-SHOW-ME-123"
	r := newRig(t)
	b := r.signedIn(t)

	b.post("/providers/key", url.Values{"provider": {"nobody"}, "key": {key}})
	page := b.get("/")

	if !strings.Contains(page.Body.String(), "does not know this provider") {
		t.Fatalf("the page does not show the refusal:\n%s", page.Body)
	}
	if strings.Contains(page.Body.String(), "NEVER-SHOW") || strings.Contains(r.log.String(), "NEVER-SHOW") {
		t.Fatal("an error page or the log holds the key")
	}
}

func TestPowers_EachFormReachesTheEdge(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	b := r.signedIn(t)

	wantRedirect(t, b.post("/allow", url.Values{"pattern": {"example.org"}, "why": {"docs"}}), "/#allow")
	if l, _ := r.edge.AllowList(t.Context()); len(l) != 1 {
		t.Fatalf("allow list = %+v", l)
	}
	wantRedirect(t, b.post("/allow/remove", url.Values{"pattern": {"example.org"}}), "/#allow")
	if l, _ := r.edge.AllowList(t.Context()); len(l) != 0 {
		t.Fatalf("allow list after remove = %+v", l)
	}
	wantRedirect(t, b.post("/providers/enable", url.Values{"provider": {"openai"}}), "/#models")
	if p, _ := r.edge.Providers(t.Context()); !p[0].Enabled {
		t.Fatal("provider not enabled")
	}
	wantRedirect(t, b.post("/providers/disable", url.Values{"provider": {"openai"}}), "/#models")
	_, _ = r.edge.SetKey(t.Context(), "openai", "sk-x")
	wantRedirect(t, b.post("/providers/key/clear", url.Values{"provider": {"openai"}}), "/#models")
	if r.edge.Key("openai") != "" {
		t.Fatal("key not cleared")
	}
	wantRedirect(t, b.post("/budget", url.Values{"total": {"25.50"}, "period": {"week"}}), "/#budget")
	if bud, _ := r.edge.Budget(t.Context()); bud.TotalMicros != 25_500_000 || bud.Period != "week" {
		t.Fatalf("budget = %+v", bud)
	}
	wantRedirect(t, b.post("/defence", url.Values{"enabled": {"on"}, "share": {"20"}}), "/#budget")
	if bud, _ := r.edge.Budget(t.Context()); !bud.Defence.Enabled || bud.Defence.SharePct != 20 {
		t.Fatalf("defence = %+v", bud.Defence)
	}
	wantRedirect(t, b.post("/freeze", nil), "/#freeze")
	if !strings.Contains(b.get("/").Body.String(), "Release the freeze") {
		t.Fatal("the page does not offer release while frozen")
	}
	wantRedirect(t, b.post("/release", nil), "/#freeze")
	if s, _ := r.edge.Status(t.Context()); s.Frozen {
		t.Fatal("release did not reach the edge")
	}
}

func TestPowers_ShowTheResultInTheSectionThatChanged(t *testing.T) {
	t.Parallel()
	b := newRig(t).signedIn(t)

	b.post("/allow", url.Values{"pattern": {"example.org"}})
	page := b.get("/").Body.String()

	section := page[strings.Index(page, `id="allow"`):strings.Index(page, `id="models"`)]
	if !strings.Contains(section, "The machine can now reach this name.") {
		t.Fatalf("the result is not in the allow list section:\n%s", section)
	}
	if strings.Count(page, "The machine can now reach this name.") != 1 {
		t.Fatal("the result is shown more than once")
	}
}

func TestPowers_ABadInputShowsAPlainMessage(t *testing.T) {
	t.Parallel()
	b := newRig(t).signedIn(t)

	b.post("/budget", url.Values{"total": {"ten dollars"}, "period": {"week"}})
	page := b.get("/").Body.String()

	if !strings.Contains(page, "write the amount in dollars") && !strings.Contains(page, "Write the amount in dollars") {
		t.Fatalf("no plain message for a bad amount:\n%s", page)
	}
}

func TestHome_LabelsEachPowerHardOrSoft(t *testing.T) {
	t.Parallel()
	page := newRig(t).signedIn(t).get("/").Body.String()

	if n := strings.Count(page, "Hard wall: the edge enforces"); n != 4 {
		t.Errorf("%d powers labelled hard, want 4 (freeze, allow list, providers and keys, total budget)", n)
	}
	defence := page[strings.Index(page, "Defence share"):]
	if !strings.Contains(defence[:400], "Soft limit") {
		t.Error("the defence share is not labelled soft")
	}
	if !strings.Contains(page, "Outside the stack: Docker or Coolify") || strings.Contains(page, `action="/stop"`) {
		t.Error("start and stop must point to Docker or Coolify, with no button")
	}
}

func TestHome_WhenTheEdgeDoesNotAnswerSaysSo(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	b := r.signedIn(t)
	r.edge.Err = controltest.ErrDown

	rec := b.get("/")

	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "The edge did not answer") {
		t.Fatalf("GET / with the edge down = %d\n%s", rec.Code, rec.Body)
	}
}

func TestAnyPage_WhenTheCredentialStoreDoesNotAnswerIsUnavailable(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.creds.Err = controltest.ErrDown

	rec := r.browser(t).get("/")

	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "The edge did not answer") {
		t.Fatalf("GET / = %d\n%s", rec.Code, rec.Body)
	}
}

func TestStatic_ServesTheStyleSheetUnderAnAddressThatChangesWithIt(t *testing.T) {
	t.Parallel()
	b := newRig(t).browser(t)
	page := b.get("/setup").Body.String()
	m := regexp.MustCompile(`href="(/static/control\.css\?v=[0-9a-f]{8})"`).FindStringSubmatch(page)
	if m == nil {
		t.Fatalf("the page links no versioned style sheet:\n%s", page)
	}

	rec := b.get(m[1])

	if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/css") {
		t.Fatalf("GET %s = %d %q", m[1], rec.Code, rec.Header().Get("Content-Type"))
	}
}

func TestUnknownPage_IsNotFound(t *testing.T) {
	t.Parallel()
	rec := newRig(t).browser(t).get("/nope")
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "not here") {
		t.Fatalf("GET /nope = %d\n%s", rec.Code, rec.Body)
	}
}

func TestRequest_WithAHugeBodyIsRefused(t *testing.T) {
	t.Parallel()
	b := newRig(t).signedIn(t)
	rec := b.post("/allow", url.Values{"pattern": {strings.Repeat("a", 70<<10)}})
	if rec.Code != http.StatusRequestEntityTooLarge && rec.Code != http.StatusForbidden {
		t.Fatalf("POST with a huge body = %d", rec.Code)
	}
}
