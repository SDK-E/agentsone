// SPDX-License-Identifier: MIT

// Package moduletest checks how a core.Module plugs into the API layer.
// Module tests use it to prove which tags a module owns and that mounted
// operations answer.
package moduletest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/SDK-E/agentsone/internal/contract/api"
	"github.com/SDK-E/agentsone/internal/core"
)

// OwnedTags returns the OpenAPI tags m serves, sorted.
func OwnedTags(m core.Module) []string {
	var tags []string
	for _, g := range m.API().Groups {
		tags = append(tags, g.Tag)
	}
	sort.Strings(tags)
	return tags
}

// AssertOwnsTags fails t unless m serves exactly want.
func AssertOwnsTags(t testing.TB, m core.Module, want ...string) {
	t.Helper()
	sort.Strings(want)
	got := OwnedTags(m)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("%s owns tags %v, want %v", m.Name(), got, want)
	}
}

// Mount serves m's groups on a fresh mux with the API layer's problem mapping.
func Mount(m core.Module) *http.ServeMux {
	mux := http.NewServeMux()
	opts := core.APIOptions{RequestError: writeBadRequest, ResponseError: core.WriteError}
	for _, g := range m.API().Groups {
		g.Register(mux, opts)
	}
	return mux
}

func writeBadRequest(w http.ResponseWriter, r *http.Request, err error) {
	core.WriteProblem(w, r, core.NewProblem(http.StatusBadRequest, "invalid", err.Error()))
}

// AssertEveryTagAnswersNotImplemented fails t unless, for every tag m owns,
// at least one operation answers 501 problem+json when mounted.
func AssertEveryTagAnswersNotImplemented(t testing.TB, m core.Module) {
	t.Helper()
	mux := Mount(m)
	for _, tag := range OwnedTags(m) {
		if !someRouteAnswers501(mux, tag) {
			t.Errorf("%s: no operation of tag %q answered 501 problem+json", m.Name(), tag)
		}
	}
}

func someRouteAnswers501(mux http.Handler, tag string) bool {
	for _, r := range api.Routes {
		if r.Tag == tag && answers501(mux, r) {
			return true
		}
	}
	return false
}

func answers501(mux http.Handler, r api.Route) bool {
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(r.Method, concretePath(r.Path), nil))
	if rec.Code != http.StatusNotImplemented || rec.Header().Get("Content-Type") != "application/problem+json" {
		return false
	}
	var p core.Problem
	return json.Unmarshal(rec.Body.Bytes(), &p) == nil && p.Status == http.StatusNotImplemented
}

// concretePath fills every {param} with a plain value.
func concretePath(template string) string {
	parts := strings.Split(template, "/")
	for i, p := range parts {
		if strings.HasPrefix(p, "{") && strings.HasSuffix(p, "}") {
			parts[i] = "x"
		}
	}
	return strings.Join(parts, "/")
}
