// SPDX-License-Identifier: MIT

// Package freezefile keeps the edge freeze switch in one file in the edge data folder: the file
// exists while all egress is frozen. A restarted edge reads it before it lets anything out.
package freezefile

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

const contents = "All egress is frozen. Release it with the edge freeze lever, not by deleting this file.\n"

// Store is an app.FreezeStore backed by one file.
type Store struct{ path string }

// New makes a store for the switch file at path.
func New(path string) *Store { return &Store{path: path} }

// Load reports whether the switch file exists. Anything other than a regular file or no file is
// an error, which the edge treats as frozen.
func (s *Store) Load(context.Context) (bool, error) {
	info, err := os.Stat(s.path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("read freeze switch: %w", err)
	case !info.Mode().IsRegular():
		return false, fmt.Errorf("read freeze switch: %s is not a regular file", s.path)
	}
	return true, nil
}

// Save writes or removes the switch file.
func (s *Store) Save(_ context.Context, frozen bool) error {
	if !frozen {
		if err := os.Remove(s.path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("release freeze switch: %w", err)
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("write freeze switch: %w", err)
	}
	if err := os.WriteFile(s.path, []byte(contents), 0o600); err != nil {
		return fmt.Errorf("write freeze switch: %w", err)
	}
	return nil
}
