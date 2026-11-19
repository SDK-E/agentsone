// SPDX-License-Identifier: MIT

// Package web serves the control page: server-rendered HTML forms, no script. Every visitor has a
// session (an HttpOnly, SameSite=Strict cookie) so every form carries a token; posts from another
// site, or without the session's token, are refused. A post changes something at the edge and
// answers with a redirect back to the page, which shows the result once.
package web

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"time"

	"github.com/SDK-E/agentsone/internal/edge/control/app"
	"github.com/SDK-E/agentsone/internal/edge/control/domain"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static
var staticFS embed.FS

// Cookie names. Over TLS the cookie takes the __Host- prefix, which the browser holds to Secure,
// Path=/ and no Domain.
const (
	cookieName       = "agentsone_control"
	secureCookieName = "__Host-agentsone_control"
)

// maxForm bounds a form body.
const maxForm = 64 << 10

// Options configure the page.
type Options struct {
	Auth   *app.Auth
	Powers *app.Powers
	Log    *slog.Logger
	// TrustedProxies may say, with X-Forwarded-Proto, that the visitor came over TLS. Empty (the
	// default) trusts no one: only a TLS connection to the control service itself counts.
	TrustedProxies []netip.Prefix
}

type server struct {
	auth    *app.Auth
	powers  *app.Powers
	log     *slog.Logger
	trusted []netip.Prefix
	pages   *template.Template
}

// New returns the control page handler.
func New(o Options) http.Handler {
	s := &server{auth: o.Auth, powers: o.Powers, log: o.Log, trusted: o.TrustedProxies}
	var static fs.FS
	css, err := staticFS.ReadFile("static/control.css")
	if err != nil {
		panic(err) // the embedded file is part of the binary
	}
	sum := sha256.Sum256(css)
	// The style sheet's address changes with its content, so a browser never keeps an old one.
	cssPath := "/static/control.css?v=" + hex.EncodeToString(sum[:4])
	s.pages = template.Must(template.New("pages").Funcs(template.FuncMap{
		"css":     func() string { return cssPath },
		"message": s.message,
		"dollars": domain.FormatDollars,
		"plain":   plainDollars,
		"when":    func(t time.Time) string { return t.UTC().Format("2 Jan 2006, 15:04 UTC") },
	}).ParseFS(templateFS, "templates/*.html"))

	static, err = fs.Sub(staticFS, "static")
	if err != nil {
		panic(err) // the embedded folder is part of the binary
	}
	mux := http.NewServeMux()
	mux.Handle("GET /static/", http.StripPrefix("/static/", cacheFor(time.Hour, http.FileServerFS(static))))
	mux.HandleFunc("GET /{$}", s.visit(s.home))
	mux.HandleFunc("GET /setup", s.visit(s.setupPage))
	mux.HandleFunc("POST /setup", s.visit(s.form(s.setup)))
	mux.HandleFunc("GET /sign-in", s.visit(s.signInPage))
	mux.HandleFunc("POST /sign-in", s.visit(s.form(s.signIn)))
	mux.HandleFunc("POST /sign-out", s.visit(s.form(s.signOut)))
	power := func(path, section string, do func(r *http.Request) (string, error)) {
		mux.HandleFunc("POST "+path, s.visit(s.form(s.signedIn(s.power(section, do)))))
	}
	power("/freeze", "freeze", s.freeze)
	power("/release", "freeze", s.release)
	power("/allow", "allow", s.allow)
	power("/allow/remove", "allow", s.disallow)
	power("/providers/key", "models", s.setKey)
	power("/providers/key/clear", "models", s.clearKey)
	power("/providers/enable", "models", s.enable)
	power("/providers/disable", "models", s.disable)
	power("/budget", "budget", s.setBudget)
	power("/defence", "budget", s.setDefence)
	mux.HandleFunc("/", s.visit(s.notFound))
	return s.headers(mux)
}

func cacheFor(d time.Duration, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age="+strconv.Itoa(int(d.Seconds())))
		h.ServeHTTP(w, r)
	})
}

// headers sets the security headers of every answer.
func (s *server) headers(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'none'; style-src 'self'; img-src 'self'; font-src 'self'; "+
			"form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		// same-origin, not no-referrer: with no-referrer browsers send "Origin: null" on form
		// posts, which hides the origin the same-site check reads. Other sites still get nothing.
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.Set("Cross-Origin-Resource-Policy", "same-origin")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=(), usb=()")
		h.Set("Cache-Control", "no-store")
		if s.secure(r) {
			h.Set("Strict-Transport-Security", "max-age=31536000")
		}
		next.ServeHTTP(w, r)
	})
}

// secure tells whether the visitor reached the page over TLS: a TLS connection, or a trusted
// proxy saying so.
func (s *server) secure(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	if len(s.trusted) == 0 || r.Header.Get("X-Forwarded-Proto") != "https" {
		return false
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	ip = ip.Unmap()
	for _, p := range s.trusted {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}

// request is one request with its visitor.
type request struct {
	*http.Request
	visit app.Visit
}

type handler func(w http.ResponseWriter, r request)

// visit finds the visitor's session and keeps the cookie in step with it.
func (s *server) visit(next handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		secure := s.secure(r)
		name := cookieName
		if secure {
			name = secureCookieName
		}
		id := ""
		if c, err := r.Cookie(name); err == nil {
			id = c.Value
		}
		v, err := s.auth.Visit(r.Context(), id)
		if err != nil {
			s.render(w, http.StatusServiceUnavailable, "problem", page{Title: "The edge did not answer", Error: s.message(err)})
			return
		}
		if v.Session.ID != id {
			s.setCookie(w, r, v.Session.ID)
		}
		next(w, request{Request: r, visit: v})
	}
}

func (s *server) setCookie(w http.ResponseWriter, r *http.Request, id string) {
	secure := s.secure(r)
	name := cookieName
	if secure {
		name = secureCookieName
	}
	// #nosec G124 -- Secure is on whenever the visitor came over TLS. Over plain HTTP (the
	// loopback default) a Secure cookie would never be sent back, and sign-in would not work.
	http.SetCookie(w, &http.Cookie{
		Name: name, Value: id, Path: "/", HttpOnly: true, Secure: secure, SameSite: http.SameSiteStrictMode,
	})
}

// form guards a post: same origin only, and the session's form token.
func (s *server) form(next handler) handler {
	return func(w http.ResponseWriter, r request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxForm)
		if err := r.ParseForm(); err != nil {
			s.render(w, http.StatusRequestEntityTooLarge, "problem", s.page(r, page{
				Title: "This form is too large", Error: "Send less text and try again.",
			}))
			return
		}
		reason := ""
		switch {
		case !sameOrigin(r.Request):
			reason = "from another site"
		case !r.visit.Session.CSRFMatches(r.PostFormValue("csrf")):
			reason = "without this session's form token"
		}
		if reason != "" {
			s.log.Warn("control page: refused a form "+reason, "path", r.URL.Path,
				"fetch_site", r.Header.Get("Sec-Fetch-Site"), "new_session", r.visit.Session.Created.Equal(r.visit.Session.LastSeen))
			s.render(w, http.StatusForbidden, "problem", s.page(r, page{
				Title: "This form has expired",
				Error: "The page was open too long, or the form came from another site. Go back to the control page and try again.",
			}))
			return
		}
		next(w, r)
	}
}

// sameOrigin refuses posts the browser marks as coming from another site.
func sameOrigin(r *http.Request) bool {
	site := r.Header.Get("Sec-Fetch-Site")
	switch site {
	case "", "same-origin", "none":
	default:
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	if origin == "null" {
		// An opaque origin: trust it only when the browser itself says the post is ours.
		return site == "same-origin"
	}
	u, err := url.Parse(origin)
	return err == nil && u.Host == r.Host
}

// signedIn sends a visitor who is not signed in to the right door.
func (s *server) signedIn(next handler) handler {
	return func(w http.ResponseWriter, r request) {
		if !r.visit.SignedIn {
			s.toDoor(w, r)
			return
		}
		next(w, r)
	}
}

func (s *server) toDoor(w http.ResponseWriter, r request) {
	if r.visit.NeedsSetup {
		redirect(w, "/setup")
		return
	}
	redirect(w, "/sign-in")
}

func redirect(w http.ResponseWriter, to string) {
	w.Header().Set("Location", to)
	w.WriteHeader(http.StatusSeeOther)
}

// page is what a template gets.
type page struct {
	Title      string
	CSRF       string
	SignedIn   bool
	Flash      domain.Flash
	Error      string
	ErrorField string
	O          app.Overview
}

func (s *server) page(r request, p page) page {
	p.CSRF = r.visit.Session.CSRF
	p.SignedIn = r.visit.SignedIn
	return p
}

func (s *server) render(w http.ResponseWriter, status int, name string, p page) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := s.pages.ExecuteTemplate(w, name, p); err != nil {
		s.log.Error("control page: render", "page", name, "err", err)
	}
}

func (s *server) home(w http.ResponseWriter, r request) {
	if !r.visit.SignedIn {
		s.toDoor(w, r)
		return
	}
	s.render(w, http.StatusOK, "home", s.page(r, page{
		Title: "Human-only powers", Flash: s.auth.TakeFlash(r.visit.Session.ID), O: s.powers.Overview(r.Context()),
	}))
}

func (s *server) setupPage(w http.ResponseWriter, r request) {
	if !r.visit.NeedsSetup {
		redirect(w, "/")
		return
	}
	s.render(w, http.StatusOK, "setup", s.page(r, page{Title: "Set up"}))
}

func (s *server) setup(w http.ResponseWriter, r request) {
	if !r.visit.NeedsSetup {
		redirect(w, "/sign-in")
		return
	}
	sess, err := s.auth.Setup(r.Context(), r.visit.Session.ID,
		r.PostFormValue("code"), r.PostFormValue("passphrase"), r.PostFormValue("confirm"))
	if err != nil {
		if errors.Is(err, app.ErrAlreadySetUp) {
			redirect(w, "/sign-in")
			return
		}
		field := "passphrase"
		switch {
		case errors.Is(err, app.ErrWrongCode):
			field = "code"
		case errors.Is(err, domain.ErrPassphraseMismatch):
			field = "confirm"
		}
		s.render(w, s.status(w, err), "setup", s.page(r, page{Title: "Set up", Error: s.message(err), ErrorField: field}))
		return
	}
	s.setCookie(w, r.Request, sess.ID)
	s.auth.SetFlash(sess.ID, domain.Flash{Kind: domain.FlashDone, Text: "The passphrase is set. You are signed in."})
	redirect(w, "/")
}

func (s *server) signInPage(w http.ResponseWriter, r request) {
	switch {
	case r.visit.NeedsSetup:
		redirect(w, "/setup")
	case r.visit.SignedIn:
		redirect(w, "/")
	default:
		s.render(w, http.StatusOK, "signin", s.page(r, page{Title: "Sign in", Flash: s.auth.TakeFlash(r.visit.Session.ID)}))
	}
}

func (s *server) signIn(w http.ResponseWriter, r request) {
	if r.visit.NeedsSetup {
		redirect(w, "/setup")
		return
	}
	sess, err := s.auth.Login(r.Context(), r.visit.Session.ID, r.PostFormValue("passphrase"))
	if err != nil {
		s.render(w, s.status(w, err), "signin", s.page(r, page{Title: "Sign in", Error: s.message(err)}))
		return
	}
	s.setCookie(w, r.Request, sess.ID)
	redirect(w, "/")
}

func (s *server) signOut(w http.ResponseWriter, r request) {
	s.auth.Logout(r.visit.Session.ID)
	redirect(w, "/sign-in")
}

// status picks the answer code of a failed sign-in or setup, and sets Retry-After for a lock.
func (s *server) status(w http.ResponseWriter, err error) int {
	var wait *app.WaitError
	switch {
	case errors.As(err, &wait):
		secs := int((wait.Wait + time.Second - 1) / time.Second)
		w.Header().Set("Retry-After", strconv.Itoa(secs))
		return http.StatusTooManyRequests
	case errors.Is(err, app.ErrWrongPassphrase):
		return http.StatusUnauthorized
	case errors.Is(err, app.ErrEdgeUnavailable):
		return http.StatusServiceUnavailable
	default:
		return http.StatusBadRequest
	}
}

// power runs one change at the edge, keeps its result for the next page, and goes back to the
// section it came from.
func (s *server) power(section string, do func(r *http.Request) (string, error)) handler {
	return func(w http.ResponseWriter, r request) {
		done, err := do(r.Request)
		f := domain.Flash{Kind: domain.FlashDone, Text: done, Where: section}
		if err != nil {
			f = domain.Flash{Kind: domain.FlashError, Text: s.message(err), Where: section}
		} else {
			// The action, never the values: a key must not reach the log.
			s.log.Info("control page: change made", "action", r.URL.Path)
		}
		s.auth.SetFlash(r.visit.Session.ID, f)
		redirect(w, "/#"+section)
	}
}

func (s *server) freeze(r *http.Request) (string, error) {
	return "Everything is frozen. Model calls and internet traffic from the machine are cut.", s.powers.Freeze(r.Context())
}

func (s *server) release(r *http.Request) (string, error) {
	return "The freeze is released. Agents go on where they stopped.", s.powers.Release(r.Context())
}

func (s *server) allow(r *http.Request) (string, error) {
	pattern := r.PostFormValue("pattern")
	if err := s.powers.Allow(r.Context(), pattern, r.PostFormValue("why")); err != nil {
		return "", err
	}
	return "The machine can now reach this name.", nil
}

func (s *server) disallow(r *http.Request) (string, error) {
	if err := s.powers.Disallow(r.Context(), r.PostFormValue("pattern")); err != nil {
		return "", err
	}
	return "The name is off the list. The machine can no longer reach it.", nil
}

func (s *server) setKey(r *http.Request) (string, error) {
	ks, err := s.powers.SetKey(r.Context(), r.PostFormValue("provider"), r.PostFormValue("key"))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("The key is saved in the edge. Its fingerprint is %s.", ks.Fingerprint), nil
}

func (s *server) clearKey(r *http.Request) (string, error) {
	return "The key is removed from the edge.", s.powers.ClearKey(r.Context(), r.PostFormValue("provider"))
}

func (s *server) enable(r *http.Request) (string, error) {
	return "The provider is on.", s.powers.Enable(r.Context(), r.PostFormValue("provider"))
}

func (s *server) disable(r *http.Request) (string, error) {
	return "The provider is off.", s.powers.Disable(r.Context(), r.PostFormValue("provider"))
}

func (s *server) setBudget(r *http.Request) (string, error) {
	return "The budget is saved.", s.powers.SetBudget(r.Context(), r.PostFormValue("total"), r.PostFormValue("period"))
}

func (s *server) setDefence(r *http.Request) (string, error) {
	return "The defence share is saved.",
		s.powers.SetDefence(r.Context(), r.PostFormValue("enabled") == "on", r.PostFormValue("share"))
}

func (s *server) notFound(w http.ResponseWriter, r request) {
	s.render(w, http.StatusNotFound, "problem", s.page(r, page{
		Title: "This page is not here", Error: "The control page has one page. Go back to it.",
	}))
}

// message says an error in plain words. Errors it does not know are logged and reported
// without detail.
func (s *server) message(err error) string {
	var refusal *app.Refusal
	var wait *app.WaitError
	switch {
	case errors.As(err, &wait):
		secs := int((wait.Wait + time.Second - 1) / time.Second)
		return fmt.Sprintf("Too many wrong tries. Try again in %d seconds.", secs)
	case errors.As(err, &refusal):
		return refusal.Detail
	case errors.Is(err, app.ErrEdgeUnavailable):
		s.log.Warn("control page: the edge did not answer", "err", err)
		return "The edge did not answer. Check that the edge service is running, then try again."
	case errors.Is(err, app.ErrWrongCode):
		return "This is not the setup code. Copy it again from the log of the control service."
	case errors.Is(err, app.ErrWrongPassphrase):
		return "This is not the passphrase."
	case errors.Is(err, domain.ErrPassphraseTooShort):
		return "The passphrase needs at least 12 characters."
	case errors.Is(err, domain.ErrPassphraseTooLong):
		return "The passphrase is too long. Use at most 1024 characters."
	case errors.Is(err, domain.ErrPassphraseMismatch):
		return "The two passphrases are not the same. Type them again."
	case errors.Is(err, domain.ErrAmount), errors.Is(err, domain.ErrAmountTooLarge),
		errors.Is(err, domain.ErrShare), errors.Is(err, domain.ErrPeriod),
		errors.Is(err, app.ErrEmptyName), errors.Is(err, app.ErrEmptyKey):
		msg := err.Error()
		return string(msg[0]-'a'+'A') + msg[1:] + "."
	default:
		s.log.Error("control page: unexpected error", "err", err)
		return "Something went wrong. The log of the control service has the reason."
	}
}

// plainDollars writes micros for an input field: no dollar sign, no separators.
func plainDollars(micros int64) string {
	whole := strconv.FormatInt(micros/1_000_000, 10)
	frac := micros % 1_000_000
	if frac == 0 {
		return whole
	}
	f := strconv.FormatInt(frac+1_000_000, 10)[1:]
	for len(f) > 2 && f[len(f)-1] == '0' {
		f = f[:len(f)-1]
	}
	return whole + "." + f
}
