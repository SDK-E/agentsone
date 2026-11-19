// SPDX-License-Identifier: MIT

package catalog_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/events/adapters/catalog"
	"github.com/SDK-E/agentsone/internal/events/domain"
)

func load(t *testing.T) *catalog.Catalog {
	t.Helper()
	c, err := catalog.Builtin()
	if err != nil {
		t.Fatalf("Builtin() error = %v", err)
	}
	return c
}

func TestBuiltinHasEveryContractType(t *testing.T) {
	c := load(t)

	if got := len(c.Types()); got != 33 {
		t.Fatalf("Builtin() has %d types, want 33", got)
	}
}

func TestEveryExampleValidates(t *testing.T) {
	c := load(t)
	for _, e := range c.Entries() {
		if err := c.ValidateData(e.Type, e.Example); err != nil {
			t.Errorf("%s: example rejected: %v", e.Type, err)
		}
	}
}

func TestValidateData(t *testing.T) {
	c := load(t)
	tests := []struct {
		name string
		typ  string
		data string
		ok   bool
	}{
		{"good data", "agentsone.auth.logout", `{"user":"alice"}`, true},
		{"missing required field", "agentsone.auth.logout", `{}`, false},
		{"extra field", "agentsone.auth.logout", `{"user":"alice","x":1}`, false},
		{"wrong type", "agentsone.auth.logout", `{"user":5}`, false},
		{"user too long", "agentsone.auth.logout", `{"user":"` + strings.Repeat("a", 33) + `"}`, false},
		{"type outside the catalog passes", "agentsone.custom.thing.happened", `{"anything":true}`, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := c.ValidateData(tt.typ, json.RawMessage(tt.data))
			if (err == nil) != tt.ok {
				t.Fatalf("ValidateData() error = %v, want ok=%v", err, tt.ok)
			}
			if err != nil && !errors.Is(err, core.ErrInvalid) {
				t.Fatalf("error %v does not wrap ErrInvalid", err)
			}
		})
	}
}

func TestScopeKind(t *testing.T) {
	c := load(t)

	kind, ok := c.ScopeKind("agentsone.auth.logout")
	if !ok || kind != "machine" {
		t.Fatalf("ScopeKind(auth.logout) = %q, %v", kind, ok)
	}
	if _, ok := c.ScopeKind("agentsone.custom.thing"); ok {
		t.Fatal("ScopeKind() knew a type outside the catalog")
	}
}

func TestEmitterOf(t *testing.T) {
	c := load(t)

	if got, ok := c.EmitterOf("agentsone.disk.usage.high"); !ok || got != "disk" {
		t.Fatalf("EmitterOf() = %q, %v", got, ok)
	}
}

func TestCatalogImplementsTheDomainPort(t *testing.T) {
	var _ domain.Catalog = load(t)
}

// TestEmbeddedCopyMatchesTheContract fails when contract/schemas/events/catalog.json
// changes without the embedded copy. Refresh it with a file copy.
func TestEmbeddedCopyMatchesTheContract(t *testing.T) {
	dir := os.Getenv("AGENTSONE_CONTRACT_DIR")
	if dir == "" {
		dir = filepath.Join("..", "..", "..", "..", "contract")
	}
	want, err := os.ReadFile(filepath.Join(dir, "schemas", "events", "catalog.json")) // #nosec G304 G703 -- contract file
	if os.IsNotExist(err) {
		t.Skip("contract catalog is not present")
	}
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile("catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatal("adapters/catalog/catalog.json differs from contract/schemas/events/catalog.json; copy the contract file over it")
	}
}
