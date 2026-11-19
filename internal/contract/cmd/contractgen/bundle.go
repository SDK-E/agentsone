// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

const distSpec = "contract/dist/openapi.json"

// writeDist turns the redocly bundle into contract/dist/openapi.json. Source
// schemas keep their own $id and $schema so they validate standalone; inside
// the bundle those keywords would re-base $ref resolution, so they go.
func writeDist(r repo, bundlePath string) error {
	raw, err := os.ReadFile(bundlePath) // #nosec G304 -- path given by scripts/gen.sh
	if err != nil {
		return fmt.Errorf("read bundle: %w", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("parse bundle: %w", err)
	}
	for key, v := range doc {
		stripSchemaIdentity(v, key)
	}
	renameFileSchemas(doc)
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(doc); err != nil {
		return fmt.Errorf("encode dist spec: %w", err)
	}
	return r.write(distSpec, b.Bytes())
}

// stripSchemaIdentity removes $id and $schema keywords in place. Under
// "properties" (and similar name maps) keys are property names, not
// keywords, so a property called "$schema" survives.
func stripSchemaIdentity(v any, parentKey string) {
	switch t := v.(type) {
	case map[string]any:
		if !namesMap[parentKey] {
			delete(t, "$id")
			delete(t, "$schema")
		}
		for k, child := range t {
			if !dataKeys[k] {
				stripSchemaIdentity(child, k)
			}
		}
	case []any:
		for _, child := range t {
			stripSchemaIdentity(child, "")
		}
	}
}

const schemaRefPrefix = "#/components/schemas/"

// renameFileSchemas gives whole-file schemas their alias names. The bundler
// names a schema file after itself ("event.schema"); the spec declares an
// alias component ({"$ref": ".../event.schema"}, named Event). The alias
// takes the content and every reference follows.
func renameFileSchemas(doc map[string]any) {
	components, _ := doc["components"].(map[string]any)
	schemas, _ := components["schemas"].(map[string]any)
	renames := map[string]string{}
	for alias, v := range schemas {
		if target, ok := pureRef(v); ok && strings.HasSuffix(target, ".schema") {
			renames[schemaRefPrefix+target] = schemaRefPrefix + alias
			schemas[alias] = schemas[target]
		}
	}
	for from := range renames {
		delete(schemas, strings.TrimPrefix(from, schemaRefPrefix))
	}
	rewriteRefs(doc, renames)
}

func pureRef(v any) (string, bool) {
	m, ok := v.(map[string]any)
	if !ok || len(m) != 1 {
		return "", false
	}
	ref, ok := m["$ref"].(string)
	if !ok || !strings.HasPrefix(ref, schemaRefPrefix) {
		return "", false
	}
	return strings.TrimPrefix(ref, schemaRefPrefix), true
}

func rewriteRefs(v any, renames map[string]string) {
	switch t := v.(type) {
	case map[string]any:
		for k, child := range t {
			if s, ok := child.(string); ok && k == "$ref" {
				t[k] = renamedRef(s, renames)
				continue
			}
			rewriteRefs(child, renames)
		}
	case []any:
		for _, child := range t {
			rewriteRefs(child, renames)
		}
	}
}

// renamedRef rewrites a ref to a renamed schema, keeping any deeper pointer.
func renamedRef(ref string, renames map[string]string) string {
	for from, to := range renames {
		if ref == from || strings.HasPrefix(ref, from+"/") {
			return to + strings.TrimPrefix(ref, from)
		}
	}
	return ref
}

// codegenView returns a copy of the spec that oapi-codegen can read. A
// schema typed with several non-null types (["string", "integer"]) becomes
// untyped, which generates an any. contract/dist keeps the exact types.
func codegenView(doc map[string]any) ([]byte, error) {
	b, err := json.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("copy spec: %w", err)
	}
	var cp map[string]any
	if err := json.Unmarshal(b, &cp); err != nil {
		return nil, fmt.Errorf("copy spec: %w", err)
	}
	loosenMultiTypes(cp)
	separateComponentNames(cp)
	return json.Marshal(cp)
}

// componentSuffixes name the component sections oapi-codegen also turns
// into Go types, with the suffix used when a name collides with a schema.
var componentSuffixes = map[string]string{
	"parameters":    "Parameter",
	"responses":     "Response",
	"requestBodies": "RequestBody",
	"headers":       "Header",
}

// separateComponentNames renames a parameter, response, request body or
// header component that shares its name with a schema, so the generated Go
// types do not collide ("Cursor" becomes "CursorParameter").
func separateComponentNames(doc map[string]any) {
	components, _ := doc["components"].(map[string]any)
	schemas, _ := components["schemas"].(map[string]any)
	renames := map[string]string{}
	for section, suffix := range componentSuffixes {
		entries, _ := components[section].(map[string]any)
		for name, v := range entries {
			if _, clash := schemas[name]; !clash {
				continue
			}
			prefix := "#/components/" + section + "/"
			renames[prefix+name] = prefix + name + suffix
			entries[name+suffix] = v
			delete(entries, name)
		}
	}
	rewriteRefs(doc, renames)
}

func loosenMultiTypes(v any) {
	switch t := v.(type) {
	case map[string]any:
		if types, ok := t["type"].([]any); ok && nonNullCount(types) > 1 {
			delete(t, "type")
		}
		for _, child := range t {
			loosenMultiTypes(child)
		}
	case []any:
		for _, child := range t {
			loosenMultiTypes(child)
		}
	}
}

func nonNullCount(types []any) int {
	n := 0
	for _, ty := range types {
		if ty != "null" {
			n++
		}
	}
	return n
}

// namesMap keys hold maps whose keys are names chosen by the author.
var namesMap = map[string]bool{
	"properties": true, "patternProperties": true, "$defs": true, "definitions": true,
	"dependentSchemas": true, "schemas": true,
}

// dataKeys hold instance data, which is left exactly as written.
var dataKeys = map[string]bool{
	"example": true, "examples": true, "const": true, "default": true, "enum": true, "value": true,
}
