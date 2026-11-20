// SPDX-License-Identifier: MIT

package httpadapter_test

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	httpadapter "github.com/SDK-E/agentsone/internal/api/adapters/http"
	"github.com/SDK-E/agentsone/internal/core/coretest"
)

var dist = fstest.MapFS{
	"index.html":    {Data: []byte("<!doctype html><title>Agents One</title>")},
	"assets/app.js": {Data: []byte("console.log(1)")},
}

func get(h http.Handler, method, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec
}

func TestUI_ServesFilesAndFallsBackToIndex(t *testing.T) {
	t.Parallel()
	ui := httpadapter.UI(dist)
	if rec := get(ui, "GET", "/assets/app.js"); rec.Code != 200 || rec.Body.String() != "console.log(1)" {
		t.Fatalf("asset: %d %s", rec.Code, rec.Body)
	}
	for _, p := range []string{"/", "/setup", "/agents/ada/chat"} {
		if rec := get(ui, "GET", p); rec.Code != 200 || !strings.Contains(rec.Body.String(), "Agents One") {
			t.Errorf("%s: %d %s", p, rec.Code, rec.Body)
		}
	}
}

func TestUI_RefusesWrites(t *testing.T) {
	t.Parallel()
	for _, method := range []string{"POST", "PUT", "PATCH", "DELETE"} {
		rec := get(httpadapter.UI(dist), method, "/setup")
		if rec.Code != http.StatusMethodNotAllowed || rec.Header().Get("Allow") != "GET, HEAD" ||
			!strings.Contains(rec.Body.String(), `"type":"urn:agentsone:problem:method-not-allowed"`) {
			t.Errorf("%s: %d Allow=%q %s", method, rec.Code, rec.Header().Get("Allow"), rec.Body)
		}
	}
}

func TestUI_SaysWhenTheInterfaceIsMissing(t *testing.T) {
	t.Parallel()
	if rec := get(httpadapter.UI(fstest.MapFS{}), "GET", "/setup"); rec.Code != http.StatusNotFound {
		t.Fatalf("got %d", rec.Code)
	}
}

func TestSplit_SendsAPIIDEAndDesktopPathsToTheModules(t *testing.T) {
	t.Parallel()
	api := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	h := httpadapter.Split(api, httpadapter.UI(dist))
	for p, want := range map[string]int{
		"/v1/agents": 418, "/ide/x": 418, "/desktop/ada/vnc": 418, "/v1x": 200, "/desktopx": 200, "/": 200,
	} {
		if rec := get(h, "GET", p); rec.Code != want {
			t.Errorf("%s: %d, want %d", p, rec.Code, want)
		}
	}
}

func TestSecurityHeaders_SetCSPOnTheInterfaceAndNoStoreOnTheAPI(t *testing.T) {
	t.Parallel()
	h := httpadapter.SecurityHeaders(httpadapter.UI(dist))
	rec := get(h, "GET", "/")
	if rec.Header().Get("Content-Security-Policy") != httpadapter.ContentSecurityPolicy ||
		rec.Header().Get("X-Frame-Options") != "DENY" || rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("headers %v", rec.Header())
	}
	rec = get(httpadapter.SecurityHeaders(http.NotFoundHandler()), "GET", "/v1/agents")
	if rec.Header().Get("Cache-Control") != "no-store" || rec.Header().Get("Content-Security-Policy") != "" {
		t.Fatalf("api headers %v", rec.Header())
	}
}

func TestRequestLog_LogsTheRequestWithoutSecrets(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	h := httpadapter.RequestLog(log, "tcp", coretest.NewClock(coretest.Epoch), http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	r := httptest.NewRequest("GET", "/v1/agents?token=aot_secret", nil)
	r.Header.Set("Authorization", "Bearer aot_secret")
	r.AddCookie(&http.Cookie{Name: httpadapter.CookieName, Value: "aos_secret"}) // #nosec G124 -- request cookie in a test; attributes are not sent
	h.ServeHTTP(httptest.NewRecorder(), r)
	line := buf.String()
	if !strings.Contains(line, "path=/v1/agents") || !strings.Contains(line, "status=200") || !strings.Contains(line, "listener=tcp") {
		t.Fatalf("log %q", line)
	}
	if strings.Contains(line, "secret") {
		t.Fatalf("secret logged: %q", line)
	}
}
