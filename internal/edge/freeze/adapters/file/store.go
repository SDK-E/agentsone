// SPDX-License-Identifier: MIT

// Package file keeps the freeze-all switch in a small JSON file in the edge data folder, so a
// freeze survives an edge restart.
package file

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/SDK-E/agentsone/internal/edge/freeze/domain"
)

// FileName is the state file inside the edge data folder.
const FileName = "freeze.json"

// Store is the state file.
type Store struct{ path string }

// New returns the store at path.
func New(path string) *Store { return &Store{path: path} }

type fileState struct {
	Frozen bool      `json:"frozen"`
	Since  time.Time `json:"since"`
}

// Load reads the state. A missing file is the zero state; a file it cannot read is an error.
func (s *Store) Load(context.Context) (domain.State, error) {
	raw, err := os.ReadFile(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return domain.State{}, nil
	}
	if err != nil {
		return domain.State{}, fmt.Errorf("freeze: read %s: %w", s.path, err)
	}
	var f fileState
	if err := json.Unmarshal(raw, &f); err != nil {
		return domain.State{}, fmt.Errorf("freeze: %s: %w", s.path, err)
	}
	return domain.State{Frozen: f.Frozen, Since: f.Since}, nil
}

// Save writes the state atomically: a synced temporary file renamed over the old one, owner only.
func (s *Store) Save(_ context.Context, st domain.State) (err error) {
	raw, err := json.Marshal(fileState{Frozen: st.Frozen, Since: st.Since.UTC()})
	if err != nil {
		return fmt.Errorf("freeze: encode state: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".freeze-*.json")
	if err != nil {
		return fmt.Errorf("freeze: save: %w", err)
	}
	defer func() {
		if err != nil {
			_ = os.Remove(tmp.Name()) //nolint:errcheck // temp file cleanup; err already reports the failed save, and a leftover dot file is never read
		}
	}()
	if _, err = tmp.Write(raw); err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(tmp.Name(), 0o600)
	}
	if err == nil {
		err = os.Rename(tmp.Name(), s.path)
	}
	if err != nil {
		return fmt.Errorf("freeze: save %s: %w", s.path, err)
	}
	return nil
}
