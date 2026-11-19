// SPDX-License-Identifier: MIT

package contract_test

import (
	"bytes"
	"fmt"
	"os"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// mapLoader serves documents by URL from memory.
type mapLoader map[string]any

func (m mapLoader) Load(url string) (any, error) {
	doc, ok := m[url]
	if !ok {
		return nil, fmt.Errorf("no document for %s", url)
	}
	return doc, nil
}

// openRPCMetaSchema compiles the vendored OpenRPC 1.3 meta-schema and the
// JSON Schema meta-schema it points to (contract/vendor/openrpc). Both files
// are kept as published. Their "$schema" keyword names a URL that is no
// published draft, so the test drops that keyword in memory and compiles them
// as draft 07, the draft they are written in.
func openRPCMetaSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft7)
	c.AssertFormat()
	// The OpenRPC file refers to the JSON Schema one both with and without a
	// trailing slash, so the loader answers both spellings.
	loader := mapLoader{}
	for _, entry := range []struct{ file, url string }{
		{"vendor/openrpc/json-schema-meta-schema.json", "https://meta.json-schema.tools"},
		{"vendor/openrpc/json-schema-meta-schema.json", "https://meta.json-schema.tools/"},
		{"vendor/openrpc/meta-schema-1.3.json", "https://meta.open-rpc.org/"},
	} {
		raw, err := os.ReadFile(contractPath(t, entry.file)) // #nosec G304 -- contract file
		if err != nil {
			t.Fatal(err)
		}
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		if m, ok := doc.(map[string]any); ok {
			delete(m, "$schema")
		}
		loader[entry.url] = doc
	}
	c.UseLoader(loader)
	schema, err := c.Compile("https://meta.open-rpc.org/")
	if err != nil {
		t.Fatal(err)
	}
	return schema
}

func validateOpenRPC(t *testing.T, schema *jsonschema.Schema, raw []byte) error {
	t.Helper()
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	return schema.Validate(doc)
}

// TestRunnerOpenRPCDocument checks runner.openrpc.json against the vendored
// OpenRPC 1.3 meta-schema.
func TestRunnerOpenRPCDocument(t *testing.T) {
	schema := openRPCMetaSchema(t)
	raw, err := os.ReadFile(contractPath(t, "schemas/runner/runner.openrpc.json")) // #nosec G304 -- contract file
	if err != nil {
		t.Fatal(err)
	}
	if err := validateOpenRPC(t, schema, raw); err != nil {
		t.Fatalf("runner.openrpc.json: %v", err)
	}
}

// TestOpenRPCMetaSchemaRejectsBadDocument proves the check can fail: a
// document without methods is not an OpenRPC document.
func TestOpenRPCMetaSchemaRejectsBadDocument(t *testing.T) {
	schema := openRPCMetaSchema(t)
	bad := []byte(`{"openrpc":"1.3.2","info":{"title":"x","version":"1"}}`)
	if err := validateOpenRPC(t, schema, bad); err == nil {
		t.Fatal("a document without methods passed the meta-schema")
	}
}
