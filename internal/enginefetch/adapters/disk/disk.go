// SPDX-License-Identifier: MIT

// Package disk keeps the engine download and the installed engine on the
// state volume: <root>/<engine>/<version>/claude.part while it downloads, then
// <root>/<engine>/<version>/claude, readable and executable by every agent
// user and writable only by its owner.
package disk

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/SDK-E/agentsone/internal/enginefetch/app"
	"github.com/SDK-E/agentsone/internal/enginefetch/domain"
)

const (
	dirMode  fs.FileMode = 0o755
	partMode fs.FileMode = 0o644
	binMode  fs.FileMode = 0o755
)

// Disk is an app.Disk on a directory.
type Disk struct{ root string }

var _ app.Disk = (*Disk)(nil)

// New returns a disk under root, normally domain.DefaultRoot. Agent users
// must be able to reach it, so it is not inside the private state folder.
func New(root string) *Disk { return &Disk{root: root} }

func (d *Disk) dir(p domain.Pin) string  { return filepath.Dir(domain.BinaryPath(d.root, p)) }
func (d *Disk) bin(p domain.Pin) string  { return domain.BinaryPath(d.root, p) }
func (d *Disk) part(p domain.Pin) string { return d.bin(p) + ".part" }

// Installed implements app.Disk.
func (d *Disk) Installed(p domain.Pin) (string, int64, error) { return sum(d.bin(p)) }

// RemoveInstalled implements app.Disk.
func (d *Disk) RemoveInstalled(p domain.Pin) error { return remove(d.bin(p)) }

// PartSize implements app.Disk.
func (d *Disk) PartSize(p domain.Pin) (int64, error) {
	fi, err := os.Stat(d.part(p))
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("stat partial download: %w", err)
	}
	return fi.Size(), nil
}

// AppendPart implements app.Disk.
func (d *Disk) AppendPart(p domain.Pin, offset int64) (io.WriteCloser, error) {
	if err := mkdirs(d.root, d.dir(p)); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(d.part(p), os.O_WRONLY|os.O_CREATE, partMode) // #nosec G304 -- the path is built from the validated pin
	if err != nil {
		return nil, fmt.Errorf("open partial download: %w", err)
	}
	if err := f.Truncate(offset); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("cut partial download: %w", err)
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("seek partial download: %w", err)
	}
	return syncCloser{f}, nil
}

// syncCloser flushes the part to disk before closing, so a power cut cannot
// leave a size that claims bytes that never landed.
type syncCloser struct{ *os.File }

func (s syncCloser) Close() error {
	serr := s.Sync()
	cerr := s.File.Close()
	if serr != nil {
		return fmt.Errorf("sync partial download: %w", serr)
	}
	if cerr != nil {
		return fmt.Errorf("close partial download: %w", cerr)
	}
	return nil
}

// PartSum implements app.Disk.
func (d *Disk) PartSum(p domain.Pin) (string, int64, error) { return sum(d.part(p)) }

// RemovePart implements app.Disk.
func (d *Disk) RemovePart(p domain.Pin) error { return remove(d.part(p)) }

// Install implements app.Disk: the part becomes the executable engine in one
// rename, so a reader never sees half a binary.
func (d *Disk) Install(p domain.Pin) error {
	if err := os.Chmod(d.part(p), binMode); err != nil {
		return fmt.Errorf("make the engine executable: %w", err)
	}
	if err := os.Rename(d.part(p), d.bin(p)); err != nil {
		return fmt.Errorf("install the engine: %w", err)
	}
	dir, err := os.Open(d.dir(p))
	if err != nil {
		return fmt.Errorf("open engine folder: %w", err)
	}
	defer func() { _ = dir.Close() }() // read-only handle
	if err := dir.Sync(); err != nil {
		return fmt.Errorf("sync engine folder: %w", err)
	}
	return nil
}

// mkdirs creates every folder from root down to dir with mode 0755, whatever
// the umask, so agent users can reach the engine.
func mkdirs(root, dir string) error {
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return fmt.Errorf("create engine folder: %w", err)
	}
	for p := dir; ; p = filepath.Dir(p) {
		if err := os.Chmod(p, dirMode); err != nil {
			return fmt.Errorf("open engine folder to agents: %w", err)
		}
		if p == filepath.Clean(root) || p == filepath.Dir(p) {
			return nil
		}
	}
}

func sum(path string) (string, int64, error) {
	f, err := os.Open(path) // #nosec G304 -- the path is built from the validated pin
	if err != nil {
		return "", 0, fmt.Errorf("open %s: %w", filepath.Base(path), err)
	}
	defer func() { _ = f.Close() }() // read-only handle
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, fmt.Errorf("read %s: %w", filepath.Base(path), err)
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

func remove(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove %s: %w", filepath.Base(path), err)
	}
	return nil
}
