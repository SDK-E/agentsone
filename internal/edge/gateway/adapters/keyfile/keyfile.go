// SPDX-License-Identifier: MIT

// Package keyfile keeps provider API keys as files in the edge volume: one
// file per provider, named by provider id, mode 0600, in a folder of mode
// 0700. Keys are written only through the admin socket; they are never read
// from environment variables and never leave the edge container.
package keyfile

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	gatewayapp "github.com/SDK-E/agentsone/internal/edge/gateway/app"
	routing "github.com/SDK-E/agentsone/internal/edge/gateway/domain"
)

// FolderName is the key folder inside the edge data folder.
const FolderName = "keys"

// Errors returned by the store.
var (
	ErrBadKey      = errors.New("keyfile: key must be 1 to 4096 printable characters without spaces")
	ErrBadProvider = errors.New("keyfile: provider id must be lowercase letters, digits and dashes")
)

var providerRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// Store implements gatewayapp.KeyStore.
type Store struct {
	dir string
}

var _ gatewayapp.KeyStore = (*Store)(nil)

// Open uses dir for keys, creating it with mode 0700.
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("keyfile: key folder: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil { //nolint:gosec // G302: a folder needs the search bit; 0700 is owner only.
		return nil, fmt.Errorf("keyfile: key folder: %w", err)
	}
	return &Store{dir: dir}, nil
}

func (s *Store) path(p routing.ProviderID) (string, error) {
	if !providerRE.MatchString(string(p)) {
		return "", fmt.Errorf("%w: %q", ErrBadProvider, p)
	}
	return filepath.Join(s.dir, string(p)), nil
}

func checkKey(k string) error {
	if k == "" || len(k) > 4096 {
		return ErrBadKey
	}
	for _, r := range k {
		if r <= ' ' || r > '~' {
			return ErrBadKey
		}
	}
	return nil
}

// Get returns the provider's key, or gatewayapp.ErrKeyNotFound.
func (s *Store) Get(_ context.Context, p routing.ProviderID) (string, error) {
	path, err := s.path(p)
	if err != nil {
		return "", err
	}
	raw, err := os.ReadFile(path) //nolint:gosec // G304: the name is a checked provider id inside the key folder.
	if errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("%w: %s", gatewayapp.ErrKeyNotFound, p)
	}
	if err != nil {
		return "", fmt.Errorf("keyfile: read key for %s: %w", p, err)
	}
	return strings.TrimSpace(string(raw)), nil
}

// Has reports whether a key is stored.
func (s *Store) Has(_ context.Context, p routing.ProviderID) (bool, error) {
	path, err := s.path(p)
	if err != nil {
		return false, err
	}
	_, err = os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("keyfile: look up key for %s: %w", p, err)
	}
	return true, nil
}

// Set stores or replaces the provider's key atomically.
func (s *Store) Set(_ context.Context, p routing.ProviderID, key string) error {
	path, err := s.path(p)
	if err != nil {
		return err
	}
	if err := checkKey(key); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.dir, ".key-*")
	if err != nil {
		return fmt.Errorf("keyfile: store key for %s: %w", p, err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }() //nolint:errcheck // temp file cleanup; after the rename it is gone, and a leftover dot file is never read
	if _, err := tmp.WriteString(key); err != nil {
		return errors.Join(fmt.Errorf("keyfile: store key for %s: %w", p, err), tmp.Close())
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("keyfile: store key for %s: %w", p, err)
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		return fmt.Errorf("keyfile: store key for %s: %w", p, err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("keyfile: store key for %s: %w", p, err)
	}
	return nil
}

// Delete removes the provider's key.
func (s *Store) Delete(_ context.Context, p routing.ProviderID) error {
	path, err := s.path(p)
	if err != nil {
		return err
	}
	err = os.Remove(path)
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("%w: %s", gatewayapp.ErrKeyNotFound, p)
	}
	if err != nil {
		return fmt.Errorf("keyfile: delete key for %s: %w", p, err)
	}
	return nil
}
