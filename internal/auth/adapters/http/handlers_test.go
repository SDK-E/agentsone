// SPDX-License-Identifier: MIT

// Handler tests run the real auth module (core.db, cheap argon2id) behind
// a mounted mux, with the caller set the way the API layer sets it.
package httpadapter_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/auth"
	contract "github.com/SDK-E/agentsone/internal/contract/api"
	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/core/coretest"
	"github.com/SDK-E/agentsone/internal/core/moduletest"
)

type daemon struct {
	t     *testing.T
	mux   http.Handler
	deps  core.Deps
	fakes *coretest.Fakes
}

func start(t *testing.T) *daemon {
	t.Helper()
	deps, fakes := coretest.NewDeps(t)
	// Cheap hashing keeps the suite fast; the defaults are RFC 9106 costs.
	cfg, err := core.ParseConfig([]byte("[auth]\nargon2_time_cost = 1\nargon2_memory_kib = 64\nargon2_threads = 1\n"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.StateDir, cfg.LibDir = deps.Config.StateDir, t.TempDir()
	deps.Config = &cfg
	dir := filepath.Join(deps.Config.LibDir, "engine")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "licence.json"), []byte(`{"engine":"claude-code","version":"v1","text":"Terms."}`), 0o600); err != nil {
		t.Fatal(err)
	}
	m := auth.New(deps)
	m.Provide(deps.Ports)
	if err := m.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return &daemon{t: t, mux: moduletest.Mount(m), deps: deps, fakes: fakes}
}

// call sends a request as caller, the way the API layer would after
// identifying it, with an optional session cookie.
func (d *daemon) call(c core.Caller, method, path, body, cookie string) *httptest.ResponseRecorder {
	d.t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	if cookie != "" {
		r.AddCookie(&http.Cookie{Name: "agentsone_session", Value: cookie}) // #nosec G124 -- request cookie in a test; attributes are not sent
	}
	r = r.WithContext(core.WithCaller(r.Context(), c))
	rec := httptest.NewRecorder()
	d.mux.ServeHTTP(rec, r)
	return rec
}

func want(t *testing.T, rec *httptest.ResponseRecorder, status int) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status %d, want %d: %s", rec.Code, status, rec.Body)
	}
}

func sessionCookie(t *testing.T, rec *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, c := range rec.Result().Cookies() {
		if c.Name == "agentsone_session" {
			return c
		}
	}
	t.Fatalf("no session cookie in %v", rec.Header())
	return nil
}

// specVersion is the version of the embedded contract.
func specVersion(t *testing.T) string {
	t.Helper()
	var doc struct {
		Info struct {
			Version string `json:"version"`
		} `json:"info"`
	}
	if err := json.Unmarshal(contract.Spec, &doc); err != nil || doc.Info.Version == "" {
		t.Fatalf("contract version %q: %v", doc.Info.Version, err)
	}
	return doc.Info.Version
}

const firstHuman = `{"name":"ada","display_name":"Ada","password":"correct horse battery"}`

func TestFirstBoot_CreateFirstHumanThenSignIn(t *testing.T) {
	t.Parallel()
	d := start(t)
	rec := d.call(core.Anonymous, http.MethodGet, "/v1/setup", "", "")
	want(t, rec, http.StatusOK)
	if !strings.Contains(rec.Body.String(), `"phase":"needs-human"`) || !strings.Contains(rec.Body.String(), `"contract_version":"`+specVersion(t)+`"`) {
		t.Fatalf("status %s", rec.Body)
	}
	want(t, d.call(core.Anonymous, http.MethodPost, "/v1/setup/first-human", firstHuman, ""), http.StatusCreated)
	want(t, d.call(core.Anonymous, http.MethodPost, "/v1/setup/first-human", strings.Replace(firstHuman, "ada", "eve", 1), ""), http.StatusConflict)

	rec = d.call(core.Anonymous, http.MethodPost, "/v1/session", `{"name":"ada","password":"correct horse battery"}`, "")
	want(t, rec, http.StatusCreated)
	c := sessionCookie(t, rec)
	if !c.HttpOnly || c.SameSite != http.SameSiteStrictMode || c.Secure || !strings.HasPrefix(c.Value, "aos_") {
		t.Fatalf("cookie %+v", c)
	}
	caller, err := d.deps.Ports.Tokens.Verify(context.Background(), c.Value)
	if err != nil || caller.Kind != core.CallerHuman || caller.Name != "ada" {
		t.Fatalf("cookie caller %+v err %v", caller, err)
	}
}

func TestSetupStatus_ShowsTheSeed(t *testing.T) {
	t.Parallel()
	d := start(t)
	rec := d.call(core.Anonymous, http.MethodGet, "/v1/setup", "", "")
	want(t, rec, http.StatusOK)
	if !strings.Contains(rec.Body.String(), `"seed_installed":false`) {
		t.Fatalf("before seeding: %s", rec.Body)
	}
	d.fakes.Seed.SetInstalled(true)
	rec = d.call(core.Anonymous, http.MethodGet, "/v1/setup", "", "")
	want(t, rec, http.StatusOK)
	if !strings.Contains(rec.Body.String(), `"seed_installed":true`) {
		t.Fatalf("after seeding: %s", rec.Body)
	}
}

func TestCreateSession_SetsASecureCookieThroughTheEdge(t *testing.T) {
	t.Parallel()
	d := start(t)
	d.call(core.Anonymous, http.MethodPost, "/v1/setup/first-human", firstHuman, "")
	r := httptest.NewRequest(http.MethodPost, "/v1/session", strings.NewReader(`{"name":"ada","password":"correct horse battery"}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Agentsone-Via", "edge")
	rec := httptest.NewRecorder()
	d.mux.ServeHTTP(rec, r)
	want(t, rec, http.StatusCreated)
	if !sessionCookie(t, rec).Secure {
		t.Fatal("cookie not Secure through the edge")
	}
}

func TestCreateSession_WrongPasswordIs401(t *testing.T) {
	t.Parallel()
	d := start(t)
	d.call(core.Anonymous, http.MethodPost, "/v1/setup/first-human", firstHuman, "")
	want(t, d.call(core.Anonymous, http.MethodPost, "/v1/session", `{"name":"ada","password":"wrong password!"}`, ""), http.StatusUnauthorized)
}

func TestCreateSession_ThrottledIs429WithRetryAfter(t *testing.T) {
	t.Parallel()
	d := start(t)
	d.call(core.Anonymous, http.MethodPost, "/v1/setup/first-human", firstHuman, "")
	var rec *httptest.ResponseRecorder
	for range 6 {
		rec = d.call(core.Anonymous, http.MethodPost, "/v1/session", `{"name":"ada","password":"wrong password!"}`, "")
	}
	wantRateLimited(t, rec)
}

func wantRateLimited(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	want(t, rec, http.StatusTooManyRequests)
	if rec.Header().Get("Retry-After") != "60" {
		t.Fatalf("retry after %q", rec.Header().Get("Retry-After"))
	}
	if !strings.Contains(rec.Body.String(), `"type":"urn:agentsone:problem:rate-limited"`) {
		t.Fatalf("problem %s", rec.Body)
	}
}

func TestUpdateHuman_WrongCurrentPasswordsAreRateLimited(t *testing.T) {
	t.Parallel()
	d := start(t)
	cookie := signIn(t, d)
	wrong := `{"password":"a brand new passphrase","current_password":"not my password"}`
	for range 5 {
		want(t, d.call(ada, http.MethodPatch, "/v1/humans/ada", wrong, cookie), http.StatusBadRequest)
	}
	right := `{"password":"a brand new passphrase","current_password":"correct horse battery"}`
	wantRateLimited(t, d.call(ada, http.MethodPatch, "/v1/humans/ada", right, cookie))
}

func signIn(t *testing.T, d *daemon) string {
	t.Helper()
	d.call(core.Anonymous, http.MethodPost, "/v1/setup/first-human", firstHuman, "")
	rec := d.call(core.Anonymous, http.MethodPost, "/v1/session", `{"name":"ada","password":"correct horse battery"}`, "")
	want(t, rec, http.StatusCreated)
	return sessionCookie(t, rec).Value
}

var ada = core.Caller{Kind: core.CallerHuman, Name: "ada", UID: -1}

func TestDeleteSession_SignsOutAndClearsTheCookie(t *testing.T) {
	t.Parallel()
	d := start(t)
	cookie := signIn(t, d)
	rec := d.call(ada, http.MethodDelete, "/v1/session", "", cookie)
	want(t, rec, http.StatusNoContent)
	if c := sessionCookie(t, rec); c.MaxAge >= 0 {
		t.Fatalf("cookie not cleared: %+v", c)
	}
	if _, err := d.deps.Ports.Tokens.Verify(context.Background(), cookie); err == nil {
		t.Fatal("session survived sign-out")
	}
	want(t, d.call(ada, http.MethodDelete, "/v1/session", "", ""), http.StatusUnauthorized)
}

func TestGetMe_DescribesAHuman(t *testing.T) {
	t.Parallel()
	d := start(t)
	signIn(t, d)
	rec := d.call(ada, http.MethodGet, "/v1/session/me", "", "")
	want(t, rec, http.StatusOK)
	var me struct {
		Kind  string `json:"kind"`
		Human struct {
			Name string `json:"name"`
		} `json:"human"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &me); err != nil || me.Kind != "human" || me.Human.Name != "ada" {
		t.Fatalf("me %s", rec.Body)
	}
}

func TestGetMe_DescribesAnAgentOnTheSocket(t *testing.T) {
	t.Parallel()
	d := start(t)
	d.fakes.Agents.Add(core.Agent{Name: "founder", Kind: core.AgentAlwaysOn, IsRoot: true, Runner: "echo"})
	rec := d.call(core.Caller{Kind: core.CallerAgent, Name: "founder", UID: 0, IsRoot: true}, http.MethodGet, "/v1/session/me", "", "")
	want(t, rec, http.StatusOK)
	if !strings.Contains(rec.Body.String(), `"via":"peer-credentials"`) || !strings.Contains(rec.Body.String(), `"kind":"agent"`) {
		t.Fatalf("me %s", rec.Body)
	}
}

func TestGetMe_DescribesAPanelToken(t *testing.T) {
	t.Parallel()
	d := start(t)
	rec := d.call(core.Caller{Kind: core.CallerPlugin, Name: "plg_01", UID: -1, Scopes: []string{"memory:read"}}, http.MethodGet, "/v1/session/me", "", "")
	want(t, rec, http.StatusOK)
	if !strings.Contains(rec.Body.String(), `"kind":"panel"`) {
		t.Fatalf("me %s", rec.Body)
	}
}

func TestGetMe_AnonymousIs401(t *testing.T) {
	t.Parallel()
	d := start(t)
	want(t, d.call(core.Caller{Kind: core.CallerSystem, Name: "x"}, http.MethodGet, "/v1/session/me", "", ""), http.StatusUnauthorized)
}

func TestHumans_AreAHumanPower(t *testing.T) {
	t.Parallel()
	d := start(t)
	signIn(t, d)
	bob := `{"name":"bob","display_name":"Bob","password":"another long passphrase"}`
	want(t, d.call(core.Caller{Kind: core.CallerAgent, Name: "founder", IsRoot: true}, http.MethodPost, "/v1/humans", bob, ""), http.StatusForbidden)
	want(t, d.call(ada, http.MethodPost, "/v1/humans", bob, ""), http.StatusCreated)
	want(t, d.call(ada, http.MethodPost, "/v1/humans", bob, ""), http.StatusConflict)
	rec := d.call(ada, http.MethodGet, "/v1/humans", "", "")
	want(t, rec, http.StatusOK)
	if !strings.Contains(rec.Body.String(), `"name":"bob"`) {
		t.Fatalf("list %s", rec.Body)
	}
	want(t, d.call(ada, http.MethodPatch, "/v1/humans/bob", `{"display_name":"Robert"}`, ""), http.StatusOK)
	rec = d.call(ada, http.MethodGet, "/v1/humans/bob", "", "")
	if !strings.Contains(rec.Body.String(), `"display_name":"Robert"`) {
		t.Fatalf("get %s", rec.Body)
	}
	want(t, d.call(core.Caller{Kind: core.CallerAgent, Name: "founder", IsRoot: true}, http.MethodDelete, "/v1/humans/bob", "", ""), http.StatusForbidden)
	want(t, d.call(ada, http.MethodDelete, "/v1/humans/bob", "", ""), http.StatusNoContent)
	want(t, d.call(ada, http.MethodGet, "/v1/humans/bob", "", ""), http.StatusNotFound)
}

func TestUpdateHuman_OwnPasswordRotatesTheSessionCookie(t *testing.T) {
	t.Parallel()
	d := start(t)
	mine := signIn(t, d)
	other := sessionCookie(t, d.call(core.Anonymous, http.MethodPost, "/v1/session", `{"name":"ada","password":"correct horse battery"}`, "")).Value
	rec := d.call(ada, http.MethodPatch, "/v1/humans/ada",
		`{"password":"a brand new passphrase","current_password":"correct horse battery"}`, mine)
	want(t, rec, http.StatusOK)
	fresh := sessionCookie(t, rec)
	if !fresh.HttpOnly || fresh.SameSite != http.SameSiteStrictMode || fresh.Value == mine || fresh.MaxAge <= 0 {
		t.Fatalf("cookie %+v", fresh)
	}
	for name, v := range map[string]string{"old": mine, "other": other} {
		if _, err := d.deps.Ports.Tokens.Verify(context.Background(), v); err == nil {
			t.Fatalf("%s session survived the password change", name)
		}
	}
	if c, err := d.deps.Ports.Tokens.Verify(context.Background(), fresh.Value); err != nil || c.Name != "ada" {
		t.Fatalf("new session %+v err %v", c, err)
	}
}

func TestUpdateHuman_OwnPasswordProblemPointsAtCurrentPassword(t *testing.T) {
	t.Parallel()
	for name, body := range map[string]string{
		"missing": `{"password":"a brand new passphrase"}`,
		"wrong":   `{"password":"a brand new passphrase","current_password":"not my password"}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			d := start(t)
			cookie := signIn(t, d)
			rec := d.call(ada, http.MethodPatch, "/v1/humans/ada", body, cookie)
			want(t, rec, http.StatusBadRequest)
			var p struct {
				Type   string `json:"type"`
				Errors []struct {
					Pointer string `json:"pointer"`
				} `json:"errors"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil || p.Type != "urn:agentsone:problem:validation" ||
				len(p.Errors) != 1 || p.Errors[0].Pointer != "/current_password" {
				t.Fatalf("problem %s", rec.Body)
			}
			if rec.Header().Get("Set-Cookie") != "" {
				t.Fatal("refused change set a cookie")
			}
		})
	}
}

func TestUpdateHuman_ResetOfAnotherHumanSetsNoCookie(t *testing.T) {
	t.Parallel()
	d := start(t)
	cookie := signIn(t, d)
	want(t, d.call(ada, http.MethodPost, "/v1/humans", `{"name":"bob","display_name":"Bob","password":"another long passphrase"}`, cookie), http.StatusCreated)
	rec := d.call(ada, http.MethodPatch, "/v1/humans/bob", `{"password":"a brand new passphrase"}`, cookie)
	want(t, rec, http.StatusOK)
	if rec.Header().Get("Set-Cookie") != "" {
		t.Fatal("reset of another human set a cookie")
	}
	if _, err := d.deps.Ports.Tokens.Verify(context.Background(), cookie); err != nil {
		t.Fatalf("caller's session ended: %v", err)
	}
}

func TestRevokeToken_EndsAToken(t *testing.T) {
	t.Parallel()
	d := start(t)
	founder := core.Caller{Kind: core.CallerAgent, Name: "founder"}
	tok, _ := d.deps.Ports.Tokens.Issue(context.Background(), founder, nil, time.Hour)
	id := tok.ID
	want(t, d.call(core.Caller{Kind: core.CallerAgent, Name: "cto"}, http.MethodDelete, "/v1/tokens/"+id, "", ""), http.StatusForbidden)
	want(t, d.call(founder, http.MethodDelete, "/v1/tokens/"+id, "", ""), http.StatusNoContent)
	want(t, d.call(founder, http.MethodDelete, "/v1/tokens/nope", "", ""), http.StatusNotFound)
}

func TestEngineLicence_AcceptedByAHuman(t *testing.T) {
	t.Parallel()
	d := start(t)
	signIn(t, d)
	rec := d.call(ada, http.MethodGet, "/v1/setup/engine-licence", "", "")
	want(t, rec, http.StatusOK)
	if !strings.Contains(rec.Body.String(), `"accepted":false`) {
		t.Fatalf("licence %s", rec.Body)
	}
	want(t, d.call(core.Caller{Kind: core.CallerAgent, Name: "founder", IsRoot: true}, http.MethodPost, "/v1/setup/engine-licence", `{"accept":true,"version":"v1"}`, ""), http.StatusForbidden)
	rec = d.call(ada, http.MethodPost, "/v1/setup/engine-licence", `{"accept":true,"version":"v1"}`, "")
	want(t, rec, http.StatusAccepted)
	if !strings.Contains(rec.Body.String(), `"state":"queued"`) {
		t.Fatalf("fetch %s", rec.Body)
	}
	rec = d.call(ada, http.MethodGet, "/v1/setup/engine-licence", "", "")
	if !strings.Contains(rec.Body.String(), `"accepted_by":"ada"`) {
		t.Fatalf("licence %s", rec.Body)
	}
	rec = d.call(ada, http.MethodGet, "/v1/setup/engine-fetch", "", "")
	want(t, rec, http.StatusOK)
	rec = d.call(core.Anonymous, http.MethodGet, "/v1/setup", "", "")
	if !strings.Contains(rec.Body.String(), `"phase":"ready"`) || !strings.Contains(rec.Body.String(), `"licence_accepted":true`) {
		t.Fatalf("status %s", rec.Body)
	}
}
