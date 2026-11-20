// SPDX-License-Identifier: MIT

// Package tokenkey keeps the gateway key: the secret that signs agent gateway tokens. The edge
// makes it on first start, as a file only the edge user can read (mode 0600, owned by the edge
// user), and uses the same key on every later start, so tokens the daemon issued stay valid
// across an edge restart.
//
// The file sits in its own folder, the gateway-key volume of compose.yaml. The edge mounts it
// inside its data folder; the machine mounts it read-only at /run/agentsone/gateway-key, so the
// daemon reads the key at /run/agentsone/gateway-key/key. Control never mounts it.
package tokenkey

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// Folder is the key folder in the edge data folder: the gateway-key volume.
const Folder = "gateway-key"

// FileName is the key file in Folder.
const FileName = "key"

// Size is the key length in bytes.
const Size = 32

// Path is the key file for the edge data folder dataDir.
func Path(dataDir string) string { return filepath.Join(dataDir, Folder, FileName) }

// LoadOrCreate returns the key at path, making it (and its folder, owner-only) first when there
// is none. Several starts at once agree on one key. A file of the wrong size is refused and left
// for a human to look at; a file others could read is made owner-only.
func LoadOrCreate(path string) ([]byte, error) {
	key, err := load(path)
	if !errors.Is(err, fs.ErrNotExist) {
		return key, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("gateway key: %w", err)
	}
	if err := create(path); err != nil && !errors.Is(err, fs.ErrExist) {
		return nil, err
	}
	return load(path)
}

func load(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
		return nil, fmt.Errorf("gateway key %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("gateway key %s: not a regular file", path)
	}
	if info.Mode().Perm() != 0o600 {
		if err := os.Chmod(path, 0o600); err != nil {
			return nil, fmt.Errorf("gateway key %s: make owner-only: %w", path, err)
		}
	}
	key, err := os.ReadFile(path) // #nosec G304 -- the path is the edge data folder
	if err != nil {
		return nil, fmt.Errorf("gateway key %s: %w", path, err)
	}
	if len(key) != Size {
		return nil, fmt.Errorf("gateway key %s: %d bytes, want %d; move the file away to make a new key "+
			"(agents then need new tokens)", path, len(key), Size)
	}
	return key, nil
}

// create writes a new key to a temporary file and links it into place, so the key file is never
// seen half written and an existing one is never replaced.
func create(path string) error {
	key := make([]byte, Size)
	if _, err := rand.Read(key); err != nil {
		return fmt.Errorf("gateway key: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".gateway-key-*")
	if err != nil {
		return fmt.Errorf("gateway key: %w", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }() //nolint:errcheck // temp file cleanup after the link; a leftover dot file is never read
	if _, err := tmp.Write(key); err != nil {
		return errors.Join(fmt.Errorf("gateway key: %w", err), tmp.Close())
	}
	if err := tmp.Sync(); err != nil {
		return errors.Join(fmt.Errorf("gateway key: %w", err), tmp.Close())
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("gateway key: %w", err)
	}
	if err := os.Link(tmp.Name(), path); err != nil {
		return fmt.Errorf("gateway key: %w", err)
	}
	return nil
}
