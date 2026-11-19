// SPDX-License-Identifier: MIT

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/contract/api"
	"github.com/SDK-E/agentsone/internal/contract/contracttest"
)

const viteOrigin = "http://127.0.0.1:5173"

var testRoutes = []api.Route{
	{Method: "GET", Path: "/v1/things", OperationID: "listThings", Tag: "things"},
	{Method: "GET", Path: "/v1/things/{id}", OperationID: "getThing", Tag: "things"},
	{Method: "POST", Path: "/v1/things", OperationID: "makeThing", Tag: "things"},
	{Method: "GET", Path: "/v1/things/stream", OperationID: "streamThings", Tag: "things", Stream: true},
}

// matchTestRoute matches full request paths, like api.Match.
func matchTestRoute(method, path string) (api.Route, bool) {
	for _, r := range testRoutes {
		if r.Method != method {
			continue
		}
		if r.Path == path || (r.OperationID == "getThing" && strings.HasPrefix(path, "/v1/things/") && path != "/v1/things/stream") {
			return r, true
		}
	}
	return api.Route{}, false
}

func fixture(op, variant string, status int, body string) contracttest.APIFixture {
	return contracttest.APIFixture{OperationID: op, Variant: variant, Status: status, Body: json.RawMessage(body)}
}

func testHandler() http.Handler {
	return NewHandler(Options{
		Fixtures: contracttest.APIFixtures{
			"listThings": {
				"":      fixture("listThings", "", 200, `{"items":[{"name":"a"}]}`),
				"empty": fixture("listThings", "empty", 200, `{"items":[]}`),
				"error": fixture("listThings", "error", 500, `{"type":"t","title":"x","status":500}`),
			},
			"getThing": {"": fixture("getThing", "", 200, `{"name":"a"}`)},
			"streamThings": {"": fixture("streamThings", "", 200,
				`[{"specversion":"1.0","id":"e1","source":"/s","type":"thing.made","sequence":1}]`)},
		},
		Match:          matchTestRoute,
		AllowedOrigins: []string{viteOrigin},
		StreamInterval: 10 * time.Millisecond,
	})
}

func serve(t *testing.T, req *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	testHandler().ServeHTTP(rec, req)
	return rec
}

func TestServesDefaultFixture(t *testing.T) {
	rec := serve(t, httptest.NewRequest(http.MethodGet, "/v1/things/7", nil))
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/json" || rec.Body.String() != `{"name":"a"}` {
		t.Fatalf("got %d %q %s", rec.Code, rec.Header().Get("Content-Type"), rec.Body)
	}
}

func TestPicksVariantByQuery(t *testing.T) {
	rec := serve(t, httptest.NewRequest(http.MethodGet, "/v1/things?fixture=empty", nil))
	if rec.Body.String() != `{"items":[]}` {
		t.Fatalf("body %s", rec.Body)
	}
}

func TestPicksVariantByHeader(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/v1/things", nil)
	req.Header.Set("X-Fixture-Variant", "error")
	rec := serve(t, req)
	if rec.Code != http.StatusInternalServerError || rec.Header().Get("Content-Type") != "application/problem+json" {
		t.Fatalf("got %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
}

// A page sets the header once for every request it makes, so operations
// without that variant serve their default fixture.
func TestHeaderVariantFallsBackToDefault(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/v1/things/7", nil)
	req.Header.Set("X-Fixture-Variant", "empty")
	rec := serve(t, req)
	if rec.Code != http.StatusOK || rec.Body.String() != `{"name":"a"}` {
		t.Fatalf("got %d %s", rec.Code, rec.Body)
	}
	if rec.Header().Get("X-Fixture-Variant-Served") != "default" {
		t.Fatalf("served header %q", rec.Header().Get("X-Fixture-Variant-Served"))
	}
}

func TestUnknownVariantNamesAvailableOnes(t *testing.T) {
	rec := serve(t, httptest.NewRequest(http.MethodGet, "/v1/things?fixture=nope", nil))
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "empty, error") {
		t.Fatalf("got %d %s", rec.Code, rec.Body)
	}
}

func TestOperationWithoutFixtureIsNotImplemented(t *testing.T) {
	rec := serve(t, httptest.NewRequest(http.MethodPost, "/v1/things", nil))
	if rec.Code != http.StatusNotImplemented || rec.Header().Get("Content-Type") != "application/problem+json" {
		t.Fatalf("got %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
}

func TestUnknownRouteIsNotFound(t *testing.T) {
	rec := serve(t, httptest.NewRequest(http.MethodGet, "/v2/elsewhere", nil))
	if rec.Code != http.StatusNotFound || rec.Header().Get("Content-Type") != "application/problem+json" {
		t.Fatalf("got %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
}

func TestPathWithoutBasePathIsNotFound(t *testing.T) {
	rec := serve(t, httptest.NewRequest(http.MethodGet, "/things/7", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("got %d, want 404 for a path without /v1", rec.Code)
	}
}

func TestCORSPreflightForViteOrigin(t *testing.T) {
	req := httptest.NewRequest(http.MethodOptions, "/v1/things", nil)
	req.Header.Set("Origin", viteOrigin)
	req.Header.Set("Access-Control-Request-Method", "GET")
	rec := serve(t, req)
	h := rec.Header()
	if rec.Code != http.StatusNoContent || h.Get("Access-Control-Allow-Origin") != viteOrigin {
		t.Fatalf("got %d %v", rec.Code, h)
	}
	if !strings.Contains(h.Get("Access-Control-Allow-Headers"), "X-Fixture-Variant") {
		t.Fatalf("allow headers %q", h.Get("Access-Control-Allow-Headers"))
	}
}

func TestNoCORSHeadersForOtherOrigins(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/v1/things", nil)
	req.Header.Set("Origin", "https://evil.example")
	rec := serve(t, req)
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("allowed origin %q", got)
	}
}

func TestStreamReplaysFixtureEvents(t *testing.T) {
	srv := httptest.NewServer(testHandler())
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/v1/things/stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if ct := res.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content type %q", ct)
	}
	lines := readEventLines(t, bufio.NewScanner(res.Body), 2)
	if lines[0] != "event: thing.made" || !strings.HasPrefix(lines[1], `data: {"specversion":"1.0"`) {
		t.Fatalf("event lines %q", lines)
	}
}

// readEventLines returns the event: and data: lines of the first n events.
func readEventLines(t *testing.T, sc *bufio.Scanner, n int) []string {
	t.Helper()
	var lines []string
	for sc.Scan() && len(lines) < n {
		line := sc.Text()
		if strings.HasPrefix(line, "event:") || strings.HasPrefix(line, "data:") {
			lines = append(lines, line)
		}
	}
	if len(lines) < n {
		t.Fatalf("stream ended early: %v", sc.Err())
	}
	return lines
}
