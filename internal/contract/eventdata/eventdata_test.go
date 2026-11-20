// SPDX-License-Identifier: MIT

package eventdata_test

import (
	"bytes"
	"encoding/json"
	"reflect"
	"sort"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/SDK-E/agentsone/internal/contract/eventdata"
	"github.com/SDK-E/agentsone/internal/events/adapters/catalog"
)

func load(t *testing.T) *catalog.Catalog {
	t.Helper()
	c, err := catalog.Builtin()
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// Every catalog type has exactly one generated data type, and no generated
// type is outside the catalog.
func TestEveryCatalogTypeHasItsDataType(t *testing.T) {
	t.Parallel()
	var got []string
	for _, d := range eventdata.All() {
		got = append(got, d.EventType())
	}
	sort.Strings(got)
	if diff := cmp.Diff(load(t).Types(), got); diff != "" {
		t.Errorf("generated types (-catalog +generated):\n%s", diff)
	}
}

// The catalog's example of each type decodes into the generated type with no
// unknown field, encodes back to the same JSON, and that JSON passes the
// catalog's own validator. So the generated field names are the schema's.
func TestTheCatalogExampleRoundTripsThroughItsDataType(t *testing.T) {
	t.Parallel()
	cat := load(t)
	byType := map[string]eventdata.Data{}
	for _, d := range eventdata.All() {
		byType[d.EventType()] = d
	}
	for _, e := range cat.Entries() {
		t.Run(e.Type, func(t *testing.T) {
			t.Parallel()
			d, ok := byType[e.Type]
			if !ok {
				t.Fatalf("no generated type for %s", e.Type)
			}
			v := reflect.New(reflect.TypeOf(d))
			dec := json.NewDecoder(bytes.NewReader(e.Example))
			dec.DisallowUnknownFields()
			if err := dec.Decode(v.Interface()); err != nil {
				t.Fatalf("example does not decode into %T: %v", d, err)
			}
			out, err := json.Marshal(v.Elem().Interface())
			if err != nil {
				t.Fatal(err)
			}
			if err := cat.ValidateData(e.Type, out); err != nil {
				t.Errorf("encoded %T fails the catalog schema: %v", d, err)
			}
			var want, got any
			if err := json.Unmarshal(e.Example, &want); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(out, &got); err != nil {
				t.Fatal(err)
			}
			// An empty optional object ("body": {}) says the same as no
			// field, and the generated map field omits it when empty.
			if diff := cmp.Diff(dropEmptyObjects(want), dropEmptyObjects(got)); diff != "" {
				t.Errorf("round trip (-example +encoded):\n%s", diff)
			}
		})
	}
}

// dropEmptyObjects removes object members whose value is an empty object.
func dropEmptyObjects(v any) any {
	m, ok := v.(map[string]any)
	if !ok {
		return v
	}
	out := make(map[string]any, len(m))
	for k, child := range m {
		child = dropEmptyObjects(child)
		if cm, isMap := child.(map[string]any); isMap && len(cm) == 0 {
			continue
		}
		out[k] = child
	}
	return out
}
