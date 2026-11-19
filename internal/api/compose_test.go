// SPDX-License-Identifier: MIT

package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SDK-E/agentsone/internal/api"
	"github.com/SDK-E/agentsone/internal/contract/api/setupapi"
	"github.com/SDK-E/agentsone/internal/core"
)

type setupDone struct{ setupapi.Unimplemented }

func (setupDone) GetSetupStatus(context.Context, setupapi.GetSetupStatusRequestObject) (setupapi.GetSetupStatusResponseObject, error) {
	return setupapi.GetSetupStatus200JSONResponse{ContractVersion: "1.0.0", Phase: "ready"}, nil
}

type fakeModule struct {
	core.Base
	name string
	api  core.API
}

func (m fakeModule) Name() string  { return m.name }
func (m fakeModule) API() core.API { return m.api }

func setupModule() fakeModule {
	return fakeModule{name: "setup", api: core.API{
		Groups: []core.APIGroup{setupapi.Group(setupDone{})},
		Routes: []core.Route{{Pattern: "GET /v1/extra", Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		})}},
	}}
}

func serve(t *testing.T, h http.Handler, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec
}

func problemStatus(t *testing.T, rec *httptest.ResponseRecorder) int {
	t.Helper()
	if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("content type %q, body %s", ct, rec.Body)
	}
	var p core.Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	return p.Status
}

func TestComposeServesImplementedOperation(t *testing.T) {
	h, err := api.Compose([]core.Module{setupModule()})
	if err != nil {
		t.Fatal(err)
	}
	rec := serve(t, h, http.MethodGet, "/v1/setup")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"phase":"ready"`) {
		t.Fatalf("got %d %s", rec.Code, rec.Body)
	}
}

func TestComposeAnswers501ForUnbuiltOperations(t *testing.T) {
	h, err := api.Compose([]core.Module{setupModule()})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/v1/setup/engine-licence", "/v1/agents"} {
		rec := serve(t, h, http.MethodGet, path)
		if rec.Code != http.StatusNotImplemented || problemStatus(t, rec) != http.StatusNotImplemented {
			t.Errorf("%s: got %d", path, rec.Code)
		}
	}
}

func TestComposeAnswers404OutsideTheContract(t *testing.T) {
	h, err := api.Compose(nil)
	if err != nil {
		t.Fatal(err)
	}
	rec := serve(t, h, http.MethodGet, "/v1/no-such-thing")
	if rec.Code != http.StatusNotFound || problemStatus(t, rec) != http.StatusNotFound {
		t.Fatalf("got %d", rec.Code)
	}
}

func TestComposeMountsExtraRoutes(t *testing.T) {
	h, err := api.Compose([]core.Module{setupModule()})
	if err != nil {
		t.Fatal(err)
	}
	if rec := serve(t, h, http.MethodGet, "/v1/extra"); rec.Code != http.StatusNoContent {
		t.Fatalf("got %d", rec.Code)
	}
}

func TestComposeRejectsATagServedTwice(t *testing.T) {
	if _, err := api.Compose([]core.Module{setupModule(), setupModule()}); err == nil {
		t.Fatal("duplicate tag accepted")
	}
}

func TestComposeRejectsAnUnknownTag(t *testing.T) {
	m := fakeModule{name: "odd", api: core.API{Groups: []core.APIGroup{{Tag: "nope", Register: func(*http.ServeMux, core.APIOptions) {}}}}}
	if _, err := api.Compose([]core.Module{m}); err == nil {
		t.Fatal("unknown tag accepted")
	}
}

func TestComposeRejectsConflictingRoutes(t *testing.T) {
	dup := fakeModule{name: "dup", api: core.API{Routes: setupModule().api.Routes}}
	if _, err := api.Compose([]core.Module{setupModule(), dup}); err == nil {
		t.Fatal("conflicting route accepted")
	}
}

func TestBadRequestWritesA400Problem(t *testing.T) {
	rec := httptest.NewRecorder()
	api.BadRequest(rec, httptest.NewRequest(http.MethodGet, "/v1/agents", nil), errors.New("limit must be a number"))
	if rec.Code != http.StatusBadRequest || problemStatus(t, rec) != http.StatusBadRequest {
		t.Fatalf("got %d", rec.Code)
	}
}
