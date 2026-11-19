// SPDX-License-Identifier: MIT

package contracttest

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// APIFixture is one golden response, from
// contract/fixtures/api/<operationId>[.<variant>].json.
type APIFixture struct {
	OperationID string
	// Variant is "" for the default fixture.
	Variant string
	Status  int
	// Body is the raw JSON body; empty when the fixture has none.
	Body json.RawMessage
	File string
}

// APIFixtures maps operation id, then variant, to a fixture.
type APIFixtures map[string]map[string]APIFixture

// LoadAPIFixtures reads every *.json fixture in dir. A missing dir returns
// an error wrapping fs.ErrNotExist.
func LoadAPIFixtures(dir string) (APIFixtures, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read fixtures: %w", err)
	}
	set := APIFixtures{}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		f, err := loadAPIFixture(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		if set[f.OperationID] == nil {
			set[f.OperationID] = map[string]APIFixture{}
		}
		set[f.OperationID][f.Variant] = f
	}
	return set, nil
}

func loadAPIFixture(path string) (APIFixture, error) {
	raw, err := os.ReadFile(path) // #nosec G304 -- fixture files chosen by the caller
	if err != nil {
		return APIFixture{}, fmt.Errorf("read fixture: %w", err)
	}
	var doc struct {
		Status *int            `json:"status"`
		Body   json.RawMessage `json:"body"`
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&doc); err != nil {
		return APIFixture{}, fmt.Errorf("parse fixture %s: %w", path, err)
	}
	if doc.Status == nil {
		return APIFixture{}, fmt.Errorf("fixture %s: %w", path, errNoStatus)
	}
	op, variant, _ := strings.Cut(strings.TrimSuffix(filepath.Base(path), ".json"), ".")
	return APIFixture{OperationID: op, Variant: variant, Status: *doc.Status, Body: doc.Body, File: path}, nil
}

var errNoStatus = errors.New(`missing "status"`)
