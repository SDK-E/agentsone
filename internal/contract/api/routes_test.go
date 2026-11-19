// SPDX-License-Identifier: MIT

package api_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/SDK-E/agentsone/internal/contract/api"
)

func TestEveryRefInDistResolves(t *testing.T) {
	var doc any
	if err := json.Unmarshal(api.Spec, &doc); err != nil {
		t.Fatal(err)
	}
	for _, ref := range collectRefs(doc) {
		if !strings.HasPrefix(ref, "#/") {
			t.Errorf("$ref %q leaves the bundle", ref)
			continue
		}
		if _, ok := resolvePointer(doc, strings.TrimPrefix(ref, "#")); !ok {
			t.Errorf("$ref %q does not resolve", ref)
		}
	}
}

func TestDistHasNoEmbeddedSchemaIdentity(t *testing.T) {
	var doc map[string]any
	if err := json.Unmarshal(api.Spec, &doc); err != nil {
		t.Fatal(err)
	}
	components, _ := doc["components"].(map[string]any)
	schemas, _ := components["schemas"].(map[string]any)
	for name, s := range schemas {
		m, _ := s.(map[string]any)
		if _, ok := m["$id"]; ok {
			t.Errorf("component schema %s still has $id", name)
		}
	}
}

func TestEveryRouteMatchesItself(t *testing.T) {
	for _, r := range api.Routes {
		concrete := strings.NewReplacer("{", "x", "}", "").Replace(r.Path)
		if _, ok := api.Match(r.Method, concrete); !ok {
			t.Errorf("%s %s did not match", r.Method, concrete)
		}
	}
	if _, ok := api.Match("GET", "/v1/no/such/route/at/all"); ok {
		t.Error("unknown path matched")
	}
}

func collectRefs(v any) []string {
	var refs []string
	switch t := v.(type) {
	case map[string]any:
		for k, child := range t {
			if s, ok := child.(string); ok && k == "$ref" {
				refs = append(refs, s)
				continue
			}
			refs = append(refs, collectRefs(child)...)
		}
	case []any:
		for _, child := range t {
			refs = append(refs, collectRefs(child)...)
		}
	}
	return refs
}

func resolvePointer(doc any, pointer string) (any, bool) {
	cur := doc
	for _, part := range strings.Split(strings.TrimPrefix(pointer, "/"), "/") {
		part = strings.NewReplacer("~1", "/", "~0", "~").Replace(part)
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		if cur, ok = m[part]; !ok {
			return nil, false
		}
	}
	return cur, true
}
