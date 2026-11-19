// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestStripSchemaIdentityKeepsNamesAndData(t *testing.T) {
	var doc map[string]any
	in := `{
		"$id": "https://x/a.json", "$schema": "https://json-schema.org/draft/2020-12/schema",
		"properties": {"$schema": {"type": "string", "$id": "https://x/p.json"}},
		"examples": [{"$id": "kept"}],
		"items": {"$id": "https://x/i.json", "type": "integer"}
	}`
	if err := json.Unmarshal([]byte(in), &doc); err != nil {
		t.Fatal(err)
	}
	stripSchemaIdentity(doc, "")
	var want map[string]any
	if err := json.Unmarshal([]byte(`{
		"properties": {"$schema": {"type": "string"}},
		"examples": [{"$id": "kept"}],
		"items": {"type": "integer"}
	}`), &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(doc, want) {
		t.Fatalf("got %v\nwant %v", doc, want)
	}
}

func TestPackageFor(t *testing.T) {
	cases := map[string]string{"agents": "agentsapi", "Charter and goals": "charterandgoalsapi", "machine-pty": "machineptyapi"}
	for tag, want := range cases {
		if got := packageFor(tag); got != want {
			t.Errorf("packageFor(%q) = %q, want %q", tag, got, want)
		}
	}
}

func TestGoName(t *testing.T) {
	for in, want := range map[string]string{"bus.send": "BusSend", "task.done": "TaskDone", "hello": "Hello"} {
		if got := goName(in); got != want {
			t.Errorf("goName(%q) = %q, want %q", in, got, want)
		}
	}
}
