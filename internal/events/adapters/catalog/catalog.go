// SPDX-License-Identifier: MIT

// Package catalog serves the built-in event catalog: which types exist, their
// usual chart scope and the JSON Schema their data must match. The catalog
// file is an embedded copy of contract/schemas/events/catalog.json; a test
// fails when the two differ.
package catalog

import (
	"bytes"
	_ "embed" // the catalog is embedded
	"encoding/json"
	"fmt"
	"sort"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/SDK-E/agentsone/internal/core"
)

//go:embed catalog.json
var builtin []byte

// Entry is one catalog type.
type Entry struct {
	Type       string          `json:"type"`
	Emitter    string          `json:"emitter"`
	ChartScope string          `json:"chartscope"`
	DataSchema json.RawMessage `json:"dataSchema"`
	Example    json.RawMessage `json:"example"`
}

type compiled struct {
	entry  Entry
	schema *jsonschema.Schema
}

// Catalog answers questions about built-in event types.
type Catalog struct {
	byType map[string]compiled
}

// Builtin loads the embedded catalog and compiles every data schema.
func Builtin() (*Catalog, error) { return Parse(builtin) }

// Parse loads a catalog document.
func Parse(raw []byte) (*Catalog, error) {
	var doc struct {
		Events []Entry `json:"events"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("parse event catalog: %w", err)
	}
	c := &Catalog{byType: make(map[string]compiled, len(doc.Events))}
	for _, e := range doc.Events {
		schema, err := compile(e)
		if err != nil {
			return nil, err
		}
		c.byType[e.Type] = compiled{entry: e, schema: schema}
	}
	return c, nil
}

func compile(e Entry) (*jsonschema.Schema, error) {
	url := "mem://catalog/" + e.Type + ".json"
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(e.DataSchema))
	if err != nil {
		return nil, fmt.Errorf("catalog %s: data schema: %w", e.Type, err)
	}
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	c.AssertFormat()
	if err := c.AddResource(url, doc); err != nil {
		return nil, fmt.Errorf("catalog %s: data schema: %w", e.Type, err)
	}
	schema, err := c.Compile(url)
	if err != nil {
		return nil, fmt.Errorf("catalog %s: data schema: %w", e.Type, err)
	}
	return schema, nil
}

// Types lists the catalog types in order.
func (c *Catalog) Types() []string {
	out := make([]string, 0, len(c.byType))
	for t := range c.byType {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// Entries lists the catalog entries in type order.
func (c *Catalog) Entries() []Entry {
	types := c.Types()
	out := make([]Entry, 0, len(types))
	for _, t := range types {
		out = append(out, c.byType[t].entry)
	}
	return out
}

// ScopeKind returns "machine", "team" or "agent" for a catalog type.
func (c *Catalog) ScopeKind(eventType string) (string, bool) {
	e, ok := c.byType[eventType]
	return e.entry.ChartScope, ok
}

// EmitterOf returns the built-in emitter that produces a catalog type.
func (c *Catalog) EmitterOf(eventType string) (string, bool) {
	e, ok := c.byType[eventType]
	return e.entry.Emitter, ok
}

// ValidateData checks data against the schema of a catalog type. A type
// outside the catalog passes.
func (c *Catalog) ValidateData(eventType string, data json.RawMessage) error {
	e, ok := c.byType[eventType]
	if !ok {
		return nil
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("%w: event data is not JSON", core.ErrInvalid)
	}
	if err := e.schema.Validate(doc); err != nil {
		return fmt.Errorf("%w: data for %s: %w", core.ErrInvalid, eventType, err)
	}
	return nil
}
