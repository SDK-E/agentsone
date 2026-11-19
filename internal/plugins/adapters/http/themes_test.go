// SPDX-License-Identifier: MIT

package httpadapter_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/SDK-E/agentsone/internal/contract/api/themesapi"
	"github.com/SDK-E/agentsone/internal/contract/contracttest"
	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/core/coretest"
	httpadapter "github.com/SDK-E/agentsone/internal/plugins/adapters/http"
	"github.com/SDK-E/agentsone/internal/plugins/adapters/memory"
	"github.com/SDK-E/agentsone/internal/plugins/app"
)

const themeFixtures = "../../../../contract/fixtures/schemas/theme/"

type themeServer struct {
	mux    http.Handler
	themes *app.Themes
}

// serveThemes mounts the themes group with caller as the request's caller; a
// zero caller means nobody signed in.
func serveThemes(t *testing.T, caller *core.Caller) themeServer {
	t.Helper()
	themes := app.NewThemes(memory.NewThemes(), coretest.NewAudit(), coretest.NewClock(coretest.Epoch), coretest.NewIDs())
	raw, err := os.ReadFile(themeFixtures + "valid/agents-one-theme-draft.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := themes.EnsureBuiltin(context.Background(), raw); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	themesapi.Group(httpadapter.NewThemesHandler(themes)).Register(mux, core.APIOptions{
		RequestError: func(w http.ResponseWriter, r *http.Request, err error) {
			core.WriteProblem(w, r, core.NewProblem(http.StatusBadRequest, "invalid", err.Error()))
		},
		ResponseError: core.WriteError,
	})
	handler := http.Handler(mux)
	if caller != nil {
		handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mux.ServeHTTP(w, r.WithContext(core.WithCaller(r.Context(), *caller)))
		})
	}
	return themeServer{mux: handler, themes: themes}
}

func ada() *core.Caller { return &core.Caller{Kind: core.CallerHuman, Name: "ada", UID: -1} }

func send(t *testing.T, h http.Handler, method, target string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, bytes.NewReader(body))
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	contracttest.CheckResponse(t, req, rec)
	return rec
}

func themeBody(t *testing.T, file string, change func(doc map[string]any)) []byte {
	t.Helper()
	raw, err := os.ReadFile(themeFixtures + file) // #nosec G304 -- fixture path chosen by the test
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if change != nil {
		change(doc)
	}
	body, err := json.Marshal(map[string]any{"theme": doc})
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func rename(name string) func(map[string]any) {
	return func(d map[string]any) { d["name"] = name }
}

type installed struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	Builtin  bool    `json:"builtin"`
	PluginID *string `json:"plugin_id"`
	Theme    struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"theme"`
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder, wantStatus int) T {
	t.Helper()
	if rec.Code != wantStatus {
		t.Fatalf("status %d, want %d: %s", rec.Code, wantStatus, rec.Body)
	}
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func problemStatus(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("status %d, want %d: %s", rec.Code, want, rec.Body)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/problem+json") {
		t.Fatalf("content type %q, want a problem", ct)
	}
}

func TestImportTheme_Returns201WithTheInstalledTheme(t *testing.T) {
	t.Parallel()
	s := serveThemes(t, ada())

	rec := send(t, s.mux, http.MethodPost, "/v1/themes", themeBody(t, "valid/agents-one-theme-draft.json", rename("Ocean")))

	got := decode[installed](t, rec, http.StatusCreated)
	if !strings.HasPrefix(got.ID, "thm_") || got.Name != "Ocean" || got.Builtin || got.PluginID != nil || got.Theme.Name != "Ocean" {
		t.Fatalf("theme = %+v", got)
	}
}

func TestImportTheme_Refusals(t *testing.T) {
	t.Parallel()
	s := serveThemes(t, ada())
	send(t, s.mux, http.MethodPost, "/v1/themes", themeBody(t, "valid/agents-one-theme-draft.json", rename("Ocean")))

	problemStatus(t, send(t, s.mux, http.MethodPost, "/v1/themes", themeBody(t, "valid/agents-one-theme-draft.json", rename("Ocean"))), http.StatusConflict)
	problemStatus(t, send(t, s.mux, http.MethodPost, "/v1/themes", themeBody(t, "invalid/contrast-ratio-below-aa.json", nil)), http.StatusBadRequest)
	problemStatus(t, send(t, s.mux, http.MethodPost, "/v1/themes", []byte(`{"theme":`)), http.StatusBadRequest)
	problemStatus(t, send(t, s.mux, http.MethodPost, "/v1/themes", themeBody(t, "valid/agents-one-theme-draft.json", func(d map[string]any) {
		d["description"] = strings.Repeat("x", 300*1024)
	})), http.StatusRequestEntityTooLarge)
}

func TestListThemes_PagesWithACursor(t *testing.T) {
	t.Parallel()
	s := serveThemes(t, ada())
	send(t, s.mux, http.MethodPost, "/v1/themes", themeBody(t, "valid/agents-one-theme-draft.json", rename("Ocean")))
	type list struct {
		Items      []installed `json:"items"`
		NextCursor *string     `json:"next_cursor"`
	}

	first := decode[list](t, send(t, s.mux, http.MethodGet, "/v1/themes?limit=1", nil), http.StatusOK)
	if len(first.Items) != 1 || !first.Items[0].Builtin || first.NextCursor == nil {
		t.Fatalf("first page = %+v", first)
	}
	second := decode[list](t, send(t, s.mux, http.MethodGet, "/v1/themes?limit=1&cursor="+*first.NextCursor, nil), http.StatusOK)
	if len(second.Items) != 1 || second.Items[0].Name != "Ocean" || second.NextCursor != nil {
		t.Fatalf("second page = %+v", second)
	}
	problemStatus(t, send(t, s.mux, http.MethodGet, "/v1/themes?cursor=garbage", nil), http.StatusBadRequest)
}

func TestGetTheme_FoundAndMissing(t *testing.T) {
	t.Parallel()
	s := serveThemes(t, ada())
	created := decode[installed](t, send(t, s.mux, http.MethodPost, "/v1/themes", themeBody(t, "valid/agents-one-theme-draft.json", rename("Ocean"))), http.StatusCreated)

	got := decode[installed](t, send(t, s.mux, http.MethodGet, "/v1/themes/"+created.ID, nil), http.StatusOK)
	if got.ID != created.ID || got.Theme.Version != "0.1.0" {
		t.Fatalf("theme = %+v", got)
	}
	problemStatus(t, send(t, s.mux, http.MethodGet, "/v1/themes/thm_00000000000000000000000000", nil), http.StatusNotFound)
}

func TestDeleteTheme_Rules(t *testing.T) {
	t.Parallel()
	s := serveThemes(t, ada())
	created := decode[installed](t, send(t, s.mux, http.MethodPost, "/v1/themes", themeBody(t, "valid/agents-one-theme-draft.json", rename("Ocean"))), http.StatusCreated)
	builtin := decode[struct {
		Items []installed `json:"items"`
	}](t, send(t, s.mux, http.MethodGet, "/v1/themes?limit=1", nil), http.StatusOK).Items[0]

	problemStatus(t, send(t, s.mux, http.MethodDelete, "/v1/themes/"+builtin.ID, nil), http.StatusConflict)
	problemStatus(t, send(t, s.mux, http.MethodDelete, "/v1/themes/thm_00000000000000000000000000", nil), http.StatusNotFound)
	send(t, s.mux, http.MethodPut, "/v1/settings/theme", []byte(`{"theme_id":"`+created.ID+`"}`))
	problemStatus(t, send(t, s.mux, http.MethodDelete, "/v1/themes/"+created.ID, nil), http.StatusConflict)
	send(t, s.mux, http.MethodPut, "/v1/settings/theme", []byte(`{"theme_id":"`+builtin.ID+`"}`))
	if rec := send(t, s.mux, http.MethodDelete, "/v1/themes/"+created.ID, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
}

type selection struct {
	ThemeID string `json:"theme_id"`
	Mode    string `json:"mode"`
}

func TestThemeSelection_DefaultsThenChanges(t *testing.T) {
	t.Parallel()
	s := serveThemes(t, ada())

	def := decode[selection](t, send(t, s.mux, http.MethodGet, "/v1/settings/theme", nil), http.StatusOK)
	if !strings.HasPrefix(def.ThemeID, "thm_") || def.Mode != "system" {
		t.Fatalf("default = %+v", def)
	}
	got := decode[selection](t, send(t, s.mux, http.MethodPut, "/v1/settings/theme", []byte(`{"mode":"dark"}`)), http.StatusOK)
	if got.ThemeID != def.ThemeID || got.Mode != "dark" {
		t.Fatalf("after set = %+v", got)
	}
	again := decode[selection](t, send(t, s.mux, http.MethodGet, "/v1/settings/theme", nil), http.StatusOK)
	if again != got {
		t.Fatalf("GET = %+v, want %+v", again, got)
	}
}

func TestSetThemeSelection_Refusals(t *testing.T) {
	t.Parallel()
	s := serveThemes(t, ada())

	problemStatus(t, send(t, s.mux, http.MethodPut, "/v1/settings/theme", []byte(`{}`)), http.StatusBadRequest)
	problemStatus(t, send(t, s.mux, http.MethodPut, "/v1/settings/theme", []byte(`{"mode":"sepia"}`)), http.StatusBadRequest)
	problemStatus(t, send(t, s.mux, http.MethodPut, "/v1/settings/theme", []byte(`{"theme_id":"thm_00000000000000000000000000"}`)), http.StatusNotFound)
}

func TestThemeSelection_NeedsASignedInHuman(t *testing.T) {
	t.Parallel()
	anonymous := serveThemes(t, nil)
	problemStatus(t, send(t, anonymous.mux, http.MethodGet, "/v1/settings/theme", nil), http.StatusUnauthorized)
	problemStatus(t, send(t, anonymous.mux, http.MethodPut, "/v1/settings/theme", []byte(`{"mode":"dark"}`)), http.StatusUnauthorized)

	agent := serveThemes(t, &core.Caller{Kind: core.CallerAgent, Name: "founder", UID: 1001})
	problemStatus(t, send(t, agent.mux, http.MethodGet, "/v1/settings/theme", nil), http.StatusUnauthorized)
}
