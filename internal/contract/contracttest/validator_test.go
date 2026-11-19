// SPDX-License-Identifier: MIT

package contracttest_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/SDK-E/agentsone/internal/contract/contracttest"
)

const testSpec = `{
  "openapi": "3.1.0",
  "info": {"title": "t", "version": "1"},
  "servers": [{"url": "http://127.0.0.1:7700/v1"}],
  "paths": {
    "/things/{id}": {
      "get": {
        "operationId": "getThing",
        "tags": ["things"],
        "responses": {
          "200": {"description": "ok", "content": {"application/json": {"schema": {"$ref": "#/components/schemas/Thing"}}}},
          "404": {"$ref": "#/components/responses/Problem"},
          "default": {"$ref": "#/components/responses/Problem"}
        }
      }
    },
    "/things": {
      "post": {
        "operationId": "makeThing",
        "tags": ["things"],
        "responses": {"204": {"description": "made"}}
      }
    }
  },
  "components": {
    "schemas": {
      "Thing": {"type": "object", "required": ["name"], "properties": {"name": {"type": "string"}, "note": {"type": ["string", "null"]}}, "additionalProperties": false},
      "Problem": {"type": "object", "required": ["type", "title", "status"], "properties": {"type": {"type": "string"}, "title": {"type": "string"}, "status": {"type": "integer"}}}
    },
    "responses": {
      "Problem": {"description": "problem", "content": {"application/problem+json": {"schema": {"$ref": "#/components/schemas/Problem"}}}}
    }
  }
}`

func newValidator(t *testing.T) *contracttest.Validator {
	t.Helper()
	v, err := contracttest.NewValidator([]byte(testSpec))
	if err != nil {
		t.Fatalf("NewValidator: %v", err)
	}
	return v
}

func TestValidateResponse(t *testing.T) {
	v := newValidator(t)
	cases := []struct {
		name    string
		op      string
		status  int
		ctype   string
		body    string
		wantErr bool
	}{
		{"valid body", "getThing", 200, "application/json", `{"name":"a","note":null}`, false},
		{"missing required", "getThing", 200, "application/json", `{"note":"x"}`, true},
		{"extra property", "getThing", 200, "application/json", `{"name":"a","x":1}`, true},
		{"problem via response ref", "getThing", 404, "application/problem+json", `{"type":"t","title":"x","status":404}`, false},
		{"falls back to default", "getThing", 500, "application/problem+json", `{"type":"t","title":"x","status":500}`, false},
		{"bad problem", "getThing", 500, "application/problem+json", `{"title":"x"}`, true},
		{"content type with charset", "getThing", 200, "application/json; charset=utf-8", `{"name":"a"}`, false},
		{"unknown operation", "nope", 200, "application/json", `{}`, true},
		{"undeclared content type", "getThing", 200, "text/plain", `hi`, true},
		{"no content declared and empty body", "makeThing", 204, "", ``, false},
		{"no content declared but body sent", "makeThing", 204, "application/json", `{}`, true},
		{"status not declared and no default", "makeThing", 200, "application/json", `{}`, true},
		{"invalid json", "getThing", 200, "application/json", `{`, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := v.ValidateResponse(c.op, c.status, c.ctype, []byte(c.body))
			if (err != nil) != c.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, c.wantErr)
			}
		})
	}
}

func TestValidateJSONResponsePicksDeclaredJSONType(t *testing.T) {
	v := newValidator(t)
	if err := v.ValidateJSONResponse("getThing", 200, []byte(`{"name":"a"}`)); err != nil {
		t.Fatalf("application/json body: %v", err)
	}
	if err := v.ValidateJSONResponse("getThing", 404, []byte(`{"type":"t","title":"x","status":404}`)); err != nil {
		t.Fatalf("problem body: %v", err)
	}
	if err := v.ValidateJSONResponse("getThing", 404, []byte(`{"title":"x"}`)); err == nil {
		t.Fatal("bad problem accepted")
	}
	if err := v.ValidateJSONResponse("makeThing", 204, nil); err != nil {
		t.Fatalf("empty response: %v", err)
	}
}

func TestValidateResponseCachesSchemas(t *testing.T) {
	v := newValidator(t)
	for range 3 {
		if err := v.ValidateResponse("getThing", 200, "application/json", []byte(`{"name":"a"}`)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestNewValidatorRejectsBadSpec(t *testing.T) {
	if _, err := contracttest.NewValidator([]byte(`{`)); err == nil {
		t.Fatal("accepted invalid JSON")
	}
}

type recordingTB struct {
	testing.TB
	failed bool
}

func (r *recordingTB) Errorf(string, ...any) { r.failed = true }
func (r *recordingTB) Helper()               {}

func jsonRecorder(status int, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	rec.Header().Set("Content-Type", "application/json")
	rec.WriteHeader(status)
	_, _ = rec.WriteString(body)
	return rec
}

func TestCheckMatchesPathTemplates(t *testing.T) {
	v := newValidator(t)
	req := httptest.NewRequest(http.MethodGet, "/v1/things/42", nil)

	v.Check(t, req, jsonRecorder(http.StatusOK, `{"name":"a"}`))

	tb := &recordingTB{TB: t}
	v.Check(tb, req, jsonRecorder(http.StatusOK, `{"name":7}`))
	if !tb.failed {
		t.Fatal("Check accepted a body that breaks the contract")
	}
}

func TestCheckRequiresTheServerBasePath(t *testing.T) {
	v := newValidator(t)
	tb := &recordingTB{TB: t}
	v.Check(tb, httptest.NewRequest(http.MethodGet, "/things/42", nil), jsonRecorder(http.StatusOK, `{"name":"a"}`))
	if !tb.failed {
		t.Fatal("Check matched a path without the /v1 base path")
	}
}

func TestCheckResponseUsesEmbeddedContract(t *testing.T) {
	tb := &recordingTB{TB: t}
	contracttest.CheckResponse(tb, httptest.NewRequest(http.MethodGet, "/not/in/the/contract", nil), jsonRecorder(http.StatusOK, `{}`))
	if !tb.failed {
		t.Fatal("CheckResponse accepted a route outside the embedded contract")
	}
}

func TestCheckRejectsUnknownRoute(t *testing.T) {
	v := newValidator(t)
	tb := &recordingTB{TB: t}
	v.Check(tb, httptest.NewRequest(http.MethodDelete, "/v1/things/42", nil), jsonRecorder(http.StatusOK, `{}`))
	if !tb.failed {
		t.Fatal("Check accepted a method outside the contract")
	}
}

func TestCheckResponseUnknownRoute(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/v1/nowhere", nil)
	res := httptest.NewRecorder()
	res.WriteHeader(http.StatusOK)
	rec := &recordingTB{TB: t}
	contracttest.CheckResponse(rec, req, res)
	if !rec.failed {
		t.Fatal("CheckResponse accepted a route outside the contract")
	}
}
