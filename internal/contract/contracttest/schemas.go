// SPDX-License-Identifier: MIT

package contracttest

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

const schemaSuffix = ".schema.json"

// SchemaSet validates documents against the contract's JSON Schemas. Every
// *.schema.json under the directory is registered by its $id, so references
// between files resolve without the network.
type SchemaSet struct {
	ids      map[string]string // schema name -> $id
	mu       sync.Mutex
	compiler *jsonschema.Compiler
	compiled map[string]*jsonschema.Schema
}

// NewSchemaSet loads every *.schema.json under dir. A schema's name is its
// file name without .schema.json; names must be unique across folders.
func NewSchemaSet(dir string) (*SchemaSet, error) {
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	c.AssertFormat()
	s := &SchemaSet{ids: map[string]string{}, compiler: c, compiled: map[string]*jsonschema.Schema{}}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, schemaSuffix) {
			return err
		}
		return s.add(path)
	})
	if err != nil {
		return nil, fmt.Errorf("load schemas from %s: %w", dir, err)
	}
	return s, nil
}

func (s *SchemaSet) add(path string) error {
	raw, err := os.ReadFile(path) // #nosec G304 -- contract files chosen by the test
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	obj, _ := doc.(map[string]any)
	id, _ := obj["$id"].(string)
	if id == "" {
		return fmt.Errorf("%s has no $id", path)
	}
	name := strings.TrimSuffix(filepath.Base(path), schemaSuffix)
	if other, dup := s.ids[name]; dup {
		return fmt.Errorf("schema name %q is used by both %s and %s", name, other, id)
	}
	s.ids[name] = id
	if err := s.compiler.AddResource(id, doc); err != nil {
		return fmt.Errorf("register %s: %w", path, err)
	}
	return nil
}

// Names lists the loaded schema names, sorted.
func (s *SchemaSet) Names() []string {
	names := make([]string, 0, len(s.ids))
	for n := range s.ids {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Validate checks doc, a JSON document, against the named schema.
func (s *SchemaSet) Validate(name string, doc []byte) error {
	schema, err := s.schema(name)
	if err != nil {
		return err
	}
	v, err := jsonschema.UnmarshalJSON(bytes.NewReader(doc))
	if err != nil {
		return fmt.Errorf("document is not JSON: %w", err)
	}
	if err := schema.Validate(v); err != nil {
		return fmt.Errorf("breaks schema %s: %w", name, err)
	}
	return nil
}

var errUnknownSchema = errors.New("unknown schema")

func (s *SchemaSet) schema(name string) (*jsonschema.Schema, error) {
	id, ok := s.ids[name]
	if !ok {
		return nil, fmt.Errorf("%w %q", errUnknownSchema, name)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if compiled, ok := s.compiled[name]; ok {
		return compiled, nil
	}
	compiled, err := s.compiler.Compile(id)
	if err != nil {
		return nil, fmt.Errorf("compile schema %s: %w", name, err)
	}
	s.compiled[name] = compiled
	return compiled, nil
}
