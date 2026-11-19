// SPDX-License-Identifier: MIT

// Package licencefile reads the engine licence the image ships, a JSON
// file {"engine", "version", "text"} under the lib folder.
package licencefile

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/SDK-E/agentsone/internal/core"
)

// Source reads one licence file.
type Source struct{ path string }

// New returns a source for path.
func New(path string) Source { return Source{path: path} }

// EngineLicence returns the licence, or core.ErrUnavailable when the file
// is absent or incomplete.
func (s Source) EngineLicence(context.Context) (engine, version, text string, err error) {
	b, err := os.ReadFile(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", "", "", fmt.Errorf("engine licence %s: %w", s.path, core.ErrUnavailable)
	}
	if err != nil {
		return "", "", "", fmt.Errorf("read engine licence: %w", err)
	}
	var doc struct {
		Engine  string `json:"engine"`
		Version string `json:"version"`
		Text    string `json:"text"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return "", "", "", fmt.Errorf("parse engine licence %s: %w", s.path, err)
	}
	if doc.Engine == "" || doc.Version == "" || doc.Text == "" {
		return "", "", "", fmt.Errorf("engine licence %s is incomplete: %w", s.path, core.ErrUnavailable)
	}
	return doc.Engine, doc.Version, doc.Text, nil
}
