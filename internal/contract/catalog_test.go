// SPDX-License-Identifier: MIT

package contract_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/SDK-E/agentsone/internal/contract/contracttest"
)

type catalogEntry struct {
	Type       string          `json:"type"`
	Emitter    string          `json:"emitter"`
	DataSchema json.RawMessage `json:"dataSchema"`
	Example    json.RawMessage `json:"example"`
}

// TestEventCatalog checks the built-in event catalog: the file fits its
// schema, every entry's data schema compiles and accepts its example, and
// the emitter is the second segment of the type (agentsone.<emitter>.…).
func TestEventCatalog(t *testing.T) {
	path := contractPath(t, "schemas/events/catalog.json")
	raw, err := os.ReadFile(path) // #nosec G304 -- contract file
	if os.IsNotExist(err) {
		t.Skip("contract/schemas/events/catalog.json does not exist yet")
	}
	if err != nil {
		t.Fatal(err)
	}
	if err := schemaSet(t).Validate("events-catalog", raw); err != nil {
		t.Fatalf("catalog: %v", err)
	}
	var catalog struct {
		Events []catalogEntry `json:"events"`
	}
	if err := json.Unmarshal(raw, &catalog); err != nil {
		t.Fatal(err)
	}
	for _, e := range catalog.Events {
		checkCatalogEntry(t, e)
	}
}

func checkCatalogEntry(t *testing.T, e catalogEntry) {
	t.Helper()
	segments := strings.Split(e.Type, ".")
	if len(segments) < 3 || segments[1] != e.Emitter {
		t.Errorf("%s: emitter %q is not the second segment of the type", e.Type, e.Emitter)
	}
	schema, err := contracttest.CompileSchema("mem://catalog/"+e.Type+".json", e.DataSchema)
	if err != nil {
		t.Errorf("%s: %v", e.Type, err)
		return
	}
	if err := contracttest.ValidateWith(schema, e.Example); err != nil {
		t.Errorf("%s example: %v", e.Type, err)
	}
}
