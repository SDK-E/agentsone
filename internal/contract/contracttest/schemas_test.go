// SPDX-License-Identifier: MIT

package contracttest_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/SDK-E/agentsone/internal/contract/contracttest"
)

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func schemaDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "common.schema.json"), `{
	  "$schema": "https://json-schema.org/draft/2020-12/schema",
	  "$id": "https://example.test/schemas/common.schema.json",
	  "$defs": {"name": {"type": "string", "pattern": "^[a-z]+$"}}
	}`)
	writeFile(t, filepath.Join(dir, "runner", "wake.schema.json"), `{
	  "$schema": "https://json-schema.org/draft/2020-12/schema",
	  "$id": "https://example.test/schemas/runner/wake.schema.json",
	  "type": "object",
	  "required": ["agent"],
	  "properties": {"agent": {"$ref": "../common.schema.json#/$defs/name"}}
	}`)
	writeFile(t, filepath.Join(dir, "runner", "runner.openrpc.json"), `{"openrpc": "1.3.2"}`)
	return dir
}

func TestSchemaSetValidatesNestedSchemaWithCrossFileRef(t *testing.T) {
	set, err := contracttest.NewSchemaSet(schemaDir(t))
	if err != nil {
		t.Fatalf("NewSchemaSet: %v", err)
	}
	if err := set.Validate("wake", []byte(`{"agent":"bob"}`)); err != nil {
		t.Fatalf("valid document rejected: %v", err)
	}
	if err := set.Validate("wake", []byte(`{"agent":"Bob!"}`)); err == nil {
		t.Fatal("document breaking a referenced definition accepted")
	}
}

func TestSchemaSetUnknownName(t *testing.T) {
	set, err := contracttest.NewSchemaSet(schemaDir(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := set.Validate("missing", []byte(`{}`)); err == nil {
		t.Fatal("unknown schema name accepted")
	}
	if got := set.Names(); len(got) != 2 || got[0] != "common" || got[1] != "wake" {
		t.Fatalf("names %v", got)
	}
}

func TestSchemaSetRejectsSchemaWithoutID(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "x.schema.json"), `{"type": "object"}`)
	if _, err := contracttest.NewSchemaSet(dir); err == nil {
		t.Fatal("schema without $id accepted")
	}
}
