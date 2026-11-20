// SPDX-License-Identifier: MIT

// Package allowfile keeps the allow list in allowlist.toml in the edge data folder and watches
// the file for changes. The file is meant to be read and edited by people:
//
//	[[allow]]
//	pattern = "deb.debian.org"
//	why = "Debian packages."
//
//	[[allow]]
//	pattern = "*.example.org" # every name below example.org, not example.org itself
//
// Writes are atomic (temporary file, then rename) and serialised across processes with an
// advisory lock on allowlist.toml.lock, so the control page and the command line can both edit
// the list while the edge reads it.
package allowfile

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"github.com/pelletier/go-toml/v2"

	"github.com/SDK-E/agentsone/internal/edge/net/domain"
)

const header = `# Agents One allow list.
#
# The machine may reach only the names below. Each entry is an exact name ("github.com") or a
# wildcard ("*.github.com") that covers every name below it but not the name itself.
# Changes apply as soon as the file is saved. Edit it here, on the control page, or with
#   agentsone edge net allowlist add|remove
#
# This limits reach. It does not stop data leaving through a site that is allowed.

`

// Store is an app.AllowListStore backed by one TOML file.
type Store struct {
	path string
	// mu serialises updates inside this process; the file lock serialises across processes.
	mu sync.Mutex
}

// New makes a store for the file at path. Nothing is read or written until first use.
func New(path string) *Store { return &Store{path: path} }

type fileFormat struct {
	Allow []fileEntry `toml:"allow"`
}

type fileEntry struct {
	Pattern string `toml:"pattern"`
	Why     string `toml:"why,omitempty"`
}

// Init writes the default list when the file does not exist yet.
func (s *Store) Init(ctx context.Context) error {
	_, err := s.update(ctx, func(l domain.AllowList, exists bool) (domain.AllowList, bool, error) {
		return l, !exists, nil
	})
	return err
}

// Load reads the file. A missing file loads as the default list. A file that is not valid TOML,
// has unknown keys or holds a bad pattern is an error, so a typo never silently changes the wall.
func (s *Store) Load(context.Context) (domain.AllowList, error) {
	l, _, err := s.read()
	return l, err
}

// Update applies change to the current list and writes the result.
func (s *Store) Update(ctx context.Context, change func(domain.AllowList) (domain.AllowList, error)) (domain.AllowList, error) {
	return s.update(ctx, func(l domain.AllowList, _ bool) (domain.AllowList, bool, error) {
		next, err := change(l)
		return next, err == nil, err
	})
}

func (s *Store) update(_ context.Context, change func(domain.AllowList, bool) (domain.AllowList, bool, error)) (domain.AllowList, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return domain.AllowList{}, fmt.Errorf("create allow list folder: %w", err)
	}
	unlock, err := lockFile(s.path + ".lock")
	if err != nil {
		return domain.AllowList{}, err
	}
	defer unlock()

	current, exists, err := s.read()
	if err != nil {
		return domain.AllowList{}, err
	}
	next, write, err := change(current, exists)
	if err != nil {
		return domain.AllowList{}, err
	}
	if write {
		if err := s.write(next); err != nil {
			return domain.AllowList{}, err
		}
	}
	return next, nil
}

func (s *Store) read() (domain.AllowList, bool, error) {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return domain.DefaultAllowList(), false, nil
	}
	if err != nil {
		return domain.AllowList{}, false, fmt.Errorf("read allow list: %w", err)
	}
	l, err := parse(data)
	if err != nil {
		return domain.AllowList{}, true, fmt.Errorf("allow list %s: %w", s.path, err)
	}
	return l, true, nil
}

func parse(data []byte) (domain.AllowList, error) {
	var f fileFormat
	if err := toml.NewDecoder(bytes.NewReader(data)).DisallowUnknownFields().Decode(&f); err != nil {
		// Syntax errors and unknown keys (a typo such as "reason" for "why") both carry a
		// position; unknown keys also carry the key.
		var decodeErr *toml.DecodeError
		if !errors.As(err, &decodeErr) {
			return domain.AllowList{}, err
		}
		row, col := decodeErr.Position()
		if key := decodeErr.Key(); len(key) > 0 {
			return domain.AllowList{}, fmt.Errorf("line %d, column %d: %s: %q", row, col, decodeErr.Error(), strings.Join(key, "."))
		}
		return domain.AllowList{}, fmt.Errorf("line %d, column %d: %s", row, col, decodeErr.Error())
	}
	entries := make([]domain.Entry, 0, len(f.Allow))
	for i, e := range f.Allow {
		p, err := domain.ParsePattern(e.Pattern)
		if err != nil {
			return domain.AllowList{}, fmt.Errorf("entry %d: %w", i+1, err)
		}
		entries = append(entries, domain.Entry{Pattern: p, Why: e.Why})
	}
	return domain.NewAllowList(entries)
}

func (s *Store) write(l domain.AllowList) error {
	var f fileFormat
	for _, e := range l.Entries() {
		f.Allow = append(f.Allow, fileEntry{Pattern: e.Pattern.String(), Why: e.Why})
	}
	var buf bytes.Buffer
	buf.WriteString(header)
	if err := toml.NewEncoder(&buf).Encode(f); err != nil {
		return fmt.Errorf("encode allow list: %w", err)
	}

	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".allowlist-*.toml")
	if err != nil {
		return fmt.Errorf("write allow list: %w", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }() //nolint:errcheck // temp file cleanup; after the rename it is gone, and a leftover dot file is never read
	if _, err := tmp.Write(buf.Bytes()); err != nil {
		return errors.Join(fmt.Errorf("write allow list: %w", err), tmp.Close())
	}
	if err := tmp.Sync(); err != nil {
		return errors.Join(fmt.Errorf("write allow list: %w", err), tmp.Close())
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write allow list: %w", err)
	}
	if err := os.Rename(tmp.Name(), s.path); err != nil {
		return fmt.Errorf("write allow list: %w", err)
	}
	return nil
}

// lockFile takes an exclusive advisory lock on path, creating it if needed.
func lockFile(path string) (func(), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600) // #nosec G304 -- path is the store's own lock file
	if err != nil {
		return nil, fmt.Errorf("open allow list lock: %w", err)
	}
	fd := int(f.Fd()) // #nosec G115 -- file descriptors fit in int
	for {
		err = syscall.Flock(fd, syscall.LOCK_EX)
		if !errors.Is(err, syscall.EINTR) {
			break
		}
	}
	if err != nil {
		return nil, errors.Join(fmt.Errorf("lock allow list: %w", err), f.Close())
	}
	return func() {
		_ = syscall.Flock(fd, syscall.LOCK_UN) //nolint:errcheck // closing the file just below releases the lock as well
		_ = f.Close()                          //nolint:errcheck // lock file only: nothing is ever written through it
	}, nil
}
