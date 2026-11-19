// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestInjectNotImplemented(t *testing.T) {
	var doc map[string]any
	in := `{
		"paths": {
			"/a": {
				"parameters": [],
				"get": {"responses": {"200": {"description": "ok"}}},
				"post": {"responses": {"501": {"description": "own"}}}
			},
			"/b": {"delete": {}}
		},
		"components": {"schemas": {"Problem": {"type": "object"}}}
	}`
	if err := json.Unmarshal([]byte(in), &doc); err != nil {
		t.Fatal(err)
	}
	injectNotImplemented(doc)

	paths := doc["paths"].(map[string]any)
	ref := map[string]any{"$ref": notImplementedRef}
	get := paths["/a"].(map[string]any)["get"].(map[string]any)["responses"].(map[string]any)
	if !reflect.DeepEqual(get["501"], ref) {
		t.Errorf("get 501 = %v, want ref", get["501"])
	}
	post := paths["/a"].(map[string]any)["post"].(map[string]any)["responses"].(map[string]any)
	if post["501"].(map[string]any)["description"] != "own" {
		t.Errorf("an operation's own 501 was replaced: %v", post["501"])
	}
	del := paths["/b"].(map[string]any)["delete"].(map[string]any)["responses"].(map[string]any)
	if !reflect.DeepEqual(del["501"], ref) {
		t.Errorf("delete without responses: 501 = %v, want ref", del["501"])
	}
	if _, ok := doc["components"].(map[string]any)["responses"].(map[string]any)[notImplementedName]; !ok {
		t.Error("the NotImplemented component was not added")
	}
}
