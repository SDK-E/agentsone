// SPDX-License-Identifier: MIT

package admin_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/SDK-E/agentsone/internal/edge/net/adapters/admin"
	"github.com/SDK-E/agentsone/internal/edge/net/app"
	"github.com/SDK-E/agentsone/internal/edge/net/nettest"
)

type freezer struct {
	frozen bool
	err    error
}

func (f *freezer) Frozen(context.Context) (bool, error) { return f.frozen, f.err }

func newServer(t *testing.T) (*httptest.Server, *freezer) {
	t.Helper()
	fr := &freezer{}
	mux := http.NewServeMux()
	admin.Register(mux, app.NewEditor(nettest.NewStore()), fr)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, fr
}

func do(t *testing.T, srv *httptest.Server, method, path, body string) (int, string, map[string]any) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, srv.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var got map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&got)
	return resp.StatusCode, resp.Header.Get("Content-Type"), got
}

func patterns(t *testing.T, body map[string]any) []string {
	t.Helper()
	var out []string
	entries, _ := body["entries"].([]any)
	for _, e := range entries {
		out = append(out, e.(map[string]any)["pattern"].(string))
	}
	return out
}

func TestAllowList_ListsTheEntriesWithReasons(t *testing.T) {
	t.Parallel()
	srv, _ := newServer(t)

	status, _, body := do(t, srv, http.MethodGet, "/v1/net/allow-list", "")

	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	got := patterns(t, body)
	if len(got) == 0 || !strings.Contains(strings.Join(got, " "), "registry.npmjs.org") {
		t.Fatalf("patterns = %v, want the defaults", got)
	}
	first := body["entries"].([]any)[0].(map[string]any)
	if first["why"] == "" {
		t.Error("entry has no reason")
	}
}

func TestAllowList_AddsAnEntry(t *testing.T) {
	t.Parallel()
	srv, _ := newServer(t)

	status, _, body := do(t, srv, http.MethodPost, "/v1/net/allow-list", `{"pattern":"*.example.com","why":"Docs."}`)

	if status != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %v", status, body)
	}
	if diff := cmp.Diff(map[string]any{"pattern": "*.example.com", "why": "Docs."}, body); diff != "" {
		t.Errorf("body (-want +got):\n%s", diff)
	}
	_, _, list := do(t, srv, http.MethodGet, "/v1/net/allow-list", "")
	if !strings.Contains(strings.Join(patterns(t, list), " "), "*.example.com") {
		t.Error("added entry not listed")
	}
}

func TestAllowList_RefusesBadAdds(t *testing.T) {
	t.Parallel()
	srv, _ := newServer(t)
	for name, tc := range map[string]struct {
		body   string
		status int
		slug   string
	}{
		"not json":    {`{`, http.StatusBadRequest, "validation"},
		"bad pattern": {`{"pattern":"exa mple","why":"x"}`, http.StatusBadRequest, "validation"},
		"bad reason":  {`{"pattern":"example.org","why":"one\ntwo"}`, http.StatusBadRequest, "validation"},
		"duplicate":   {`{"pattern":"registry.npmjs.org","why":"again"}`, http.StatusConflict, "conflict"},
		"extra field": {`{"pattern":"example.org","why":"x","allow":"all"}`, http.StatusBadRequest, "validation"},
	} {
		status, ctype, body := do(t, srv, http.MethodPost, "/v1/net/allow-list", tc.body)
		if status != tc.status || ctype != "application/problem+json" || body["type"] != "urn:agentsone:problem:"+tc.slug {
			t.Errorf("%s: %d %s %v, want %d problem %s", name, status, ctype, body, tc.status, tc.slug)
		}
	}
}

func TestAllowList_RemovesAnEntry(t *testing.T) {
	t.Parallel()
	srv, _ := newServer(t)

	status, _, _ := do(t, srv, http.MethodDelete, "/v1/net/allow-list/registry.npmjs.org", "")

	if status != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", status)
	}
	_, _, list := do(t, srv, http.MethodGet, "/v1/net/allow-list", "")
	if strings.Contains(strings.Join(patterns(t, list), " "), "registry.npmjs.org") {
		t.Error("removed entry still listed")
	}
}

func TestAllowList_RemovingAnUnknownEntryIsNotFound(t *testing.T) {
	t.Parallel()
	srv, _ := newServer(t)

	status, _, body := do(t, srv, http.MethodDelete, "/v1/net/allow-list/nowhere.example", "")

	if status != http.StatusNotFound || body["type"] != "urn:agentsone:problem:not-found" {
		t.Fatalf("%d %v, want 404 not-found", status, body)
	}
}

func TestFreeze_ReportsTheSwitch(t *testing.T) {
	t.Parallel()
	srv, fr := newServer(t)

	if _, _, body := do(t, srv, http.MethodGet, "/v1/net/freeze", ""); body["frozen"] != false {
		t.Fatalf("before = %v, want not frozen", body)
	}
	fr.frozen = true
	if status, _, body := do(t, srv, http.MethodGet, "/v1/net/freeze", ""); status != http.StatusOK || body["frozen"] != true {
		t.Fatalf("after = %d %v, want frozen", status, body)
	}
}

func TestFreeze_CannotBeFlippedHere(t *testing.T) {
	t.Parallel()
	srv, fr := newServer(t)

	status, _, _ := do(t, srv, http.MethodPut, "/v1/net/freeze", `{"frozen":true}`)

	if status != http.StatusMethodNotAllowed || fr.frozen {
		t.Fatalf("PUT = %d, frozen %v; want 405 and no change (the audited freeze route flips it)", status, fr.frozen)
	}
}

func TestFreeze_ReportsASwitchThatCannotBeRead(t *testing.T) {
	t.Parallel()
	srv, fr := newServer(t)
	fr.err = errors.New("disk full")

	status, _, body := do(t, srv, http.MethodGet, "/v1/net/freeze", "")

	if status != http.StatusInternalServerError || body["type"] != "urn:agentsone:problem:internal" {
		t.Fatalf("%d %v, want 500 internal", status, body)
	}
	if strings.Contains(body["detail"].(string), "disk full") {
		t.Error("the problem leaks the internal error")
	}
}
