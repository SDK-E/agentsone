// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestInjectForbidden(t *testing.T) {
	var doc map[string]any
	in := `{
		"security": [{"cookieAuth": []}, {"bearerAuth": []}],
		"paths": {
			"/inherits": {"get": {"responses": {"200": {"description": "ok"}}}},
			"/own": {"post": {"security": [{"bearerAuth": ["tasks:write"]}], "responses": {"403": {"description": "own"}}}},
			"/public": {"get": {"security": [], "responses": {}}},
			"/optional": {"get": {"security": [{}, {"bearerAuth": ["tasks:read"]}]}},
			"/bare": {"delete": {"security": [{"hostDoor": []}]}}
		},
		"components": {"responses": {"Forbidden": {"description": "no"}}}
	}`
	if err := json.Unmarshal([]byte(in), &doc); err != nil {
		t.Fatal(err)
	}
	if err := injectForbidden(doc); err != nil {
		t.Fatal(err)
	}
	ref := map[string]any{"$ref": forbiddenRef}
	responses := func(path, method string) map[string]any {
		op := doc["paths"].(map[string]any)[path].(map[string]any)[method].(map[string]any)
		r, _ := op["responses"].(map[string]any)
		return r
	}
	if got := responses("/inherits", "get")["403"]; !reflect.DeepEqual(got, ref) {
		t.Errorf("inherited security: 403 = %v, want ref", got)
	}
	if got := responses("/own", "post")["403"].(map[string]any)["description"]; got != "own" {
		t.Errorf("an operation's own 403 was replaced: %v", got)
	}
	if _, has := responses("/public", "get")["403"]; has {
		t.Error("a public operation got a 403")
	}
	if r := responses("/optional", "get"); r != nil {
		t.Errorf("an operation with an empty requirement got responses %v", r)
	}
	if got := responses("/bare", "delete")["403"]; !reflect.DeepEqual(got, ref) {
		t.Errorf("operation without responses: 403 = %v, want ref", got)
	}
}

func TestInjectForbiddenNeedsTheComponent(t *testing.T) {
	doc := map[string]any{"paths": map[string]any{}}
	if err := injectForbidden(doc); err == nil {
		t.Fatal("no error without components.responses.Forbidden")
	}
}
