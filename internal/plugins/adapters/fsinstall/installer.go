// SPDX-License-Identifier: MIT

// Package fsinstall puts plugins on disk. A plugin lives in
// <PluginsDir>/<name>/<version>, is written through a temporary directory and
// appears with one rename, so a crash never leaves a half-written plugin.
package fsinstall

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/SDK-E/agentsone/internal/plugins/domain"
)

// ManifestFile is the normalised manifest at the root of an installed plugin.
const ManifestFile = "agentsone.plugin.json"

const (
	dirMode  = 0o750
	fileMode = 0o644
	execMode = 0o755
	tmpPref  = ".install-"
)

var nameRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)

// Installer implements domain.Installer on the local file system.
type Installer struct {
	root   string
	limits domain.InstallLimits
}

var _ domain.Installer = (*Installer)(nil)

// New returns an installer for the plugins directory. A zero limit falls back
// to the matching domain.DefaultInstallLimits value.
func New(pluginsDir string, limits domain.InstallLimits) *Installer {
	d := domain.DefaultInstallLimits
	if limits.MaxFiles <= 0 {
		limits.MaxFiles = d.MaxFiles
	}
	if limits.MaxFileBytes <= 0 {
		limits.MaxFileBytes = d.MaxFileBytes
	}
	if limits.MaxTotalSize <= 0 {
		limits.MaxTotalSize = d.MaxTotalSize
	}
	return &Installer{root: pluginsDir, limits: limits}
}

// Install writes the plugin and returns where it landed and its digest.
func (in *Installer) Install(ctx context.Context, req domain.InstallRequest) (res domain.InstallResult, err error) {
	if err := checkIdentity(req.Name, req.Version); err != nil {
		return res, err
	}
	prefix, err := cleanPrefix(req.Prefix)
	if err != nil {
		return res, err
	}
	extra, err := cleanExtra(req.Extra)
	if err != nil {
		return res, err
	}
	nameDir := filepath.Join(in.root, req.Name)
	target := filepath.Join(nameDir, req.Version)
	if _, statErr := os.Lstat(target); statErr == nil {
		return res, fmt.Errorf("%s %s: %w", req.Name, req.Version, domain.ErrAlreadyInstalled)
	}
	if err := os.MkdirAll(in.root, dirMode); err != nil {
		return res, fmt.Errorf("create plugins folder: %w", err)
	}
	tmp, err := os.MkdirTemp(in.root, tmpPref)
	if err != nil {
		return res, fmt.Errorf("create temporary folder: %w", err)
	}
	madeNameDir := false
	defer func() {
		if err != nil {
			_ = os.RemoveAll(tmp)
			if madeNameDir {
				_ = os.Remove(nameDir) // only succeeds when empty
			}
		}
	}()

	w := &writer{root: tmp, limits: in.limits, ctx: ctx}
	if err = w.copyTree(req.Tree, prefix); err != nil {
		return res, err
	}
	if err = w.writeFiles(extra); err != nil {
		return res, err
	}
	if err = w.writeManifest(req.Manifest); err != nil {
		return res, err
	}
	digest, err := TreeDigest(tmp)
	if err != nil {
		return res, err
	}
	if _, statErr := os.Stat(nameDir); errors.Is(statErr, fs.ErrNotExist) {
		madeNameDir = true
	}
	if err = os.MkdirAll(nameDir, dirMode); err != nil {
		return res, fmt.Errorf("create plugin folder: %w", err)
	}
	if err = os.Rename(tmp, target); err != nil {
		if _, statErr := os.Lstat(target); statErr == nil {
			err = fmt.Errorf("%s %s: %w", req.Name, req.Version, domain.ErrAlreadyInstalled)
		} else {
			err = fmt.Errorf("move plugin into place: %w", err)
		}
		return res, err
	}
	return domain.InstallResult{Dir: target, SHA256: digest}, nil
}

// Verify recomputes the digest of an installed plugin directory.
func (in *Installer) Verify(dir string) (string, error) { return TreeDigest(dir) }

// Remove deletes one installed version and its name folder when it is empty.
func (in *Installer) Remove(name, version string) error {
	if err := checkIdentity(name, version); err != nil {
		return err
	}
	nameDir := filepath.Join(in.root, name)
	target := filepath.Join(nameDir, version)
	if _, err := os.Lstat(target); errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("%s %s: %w", name, version, domain.ErrNotFound)
	}
	if err := os.RemoveAll(target); err != nil {
		return fmt.Errorf("remove %s %s: %w", name, version, err)
	}
	_ = os.Remove(nameDir) // only succeeds when empty
	return nil
}

func checkIdentity(name, version string) error {
	if !nameRe.MatchString(name) {
		return fmt.Errorf("plugin name %q: %w", name, domain.ErrUnsafePath)
	}
	if !domain.IsSemver(version) {
		return fmt.Errorf("plugin version %q: %w", version, domain.ErrNotSemantic)
	}
	return nil
}

func cleanPrefix(p string) (string, error) {
	if p == "" {
		return "", nil
	}
	if !fs.ValidPath(p) || p == "." {
		return "", fmt.Errorf("prefix %q: %w", p, domain.ErrUnsafePath)
	}
	return p, nil
}

func cleanExtra(files []domain.File) ([]domain.File, error) {
	for _, f := range files {
		if !fs.ValidPath(f.Path) || f.Path == "." || strings.Contains(f.Path, "\\") {
			return nil, fmt.Errorf("generated file %q: %w", f.Path, domain.ErrUnsafePath)
		}
	}
	return files, nil
}

// writer copies files into the temporary directory and enforces the limits.
type writer struct {
	ctx    context.Context
	root   string
	limits domain.InstallLimits
	count  int
	total  int64
}

func (w *writer) copyTree(src fs.FS, prefix string) error {
	if src == nil {
		return nil
	}
	return fs.WalkDir(src, ".", func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := w.ctx.Err(); err != nil {
			return err
		}
		if p == "." {
			return nil
		}
		dest := path.Join(prefix, p)
		if err := w.countEntry(p); err != nil {
			return err
		}
		switch {
		case d.IsDir():
			return os.MkdirAll(w.abs(dest), dirMode)
		case d.Type()&fs.ModeSymlink != 0:
			return w.copyLink(src, p, dest)
		case d.Type().IsRegular():
			return w.copyFile(src, p, dest, d)
		}
		return fmt.Errorf("%s is not a regular file: %w", p, domain.ErrUnsafePath)
	})
}

func (w *writer) abs(rel string) string { return filepath.Join(w.root, filepath.FromSlash(rel)) }

func (w *writer) countEntry(name string) error {
	w.count++
	if w.count > w.limits.MaxFiles {
		return fmt.Errorf("more than %d files (at %s): %w", w.limits.MaxFiles, name, domain.ErrTooLarge)
	}
	return nil
}

func (w *writer) copyFile(src fs.FS, p, dest string, d fs.DirEntry) error {
	info, err := d.Info()
	if err != nil {
		return err
	}
	mode := os.FileMode(fileMode)
	if info.Mode().Perm()&0o111 != 0 {
		mode = execMode
	}
	in, err := src.Open(p)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	return w.create(dest, in, mode, p)
}

func (w *writer) create(dest string, in io.Reader, mode os.FileMode, label string) error {
	abs := w.abs(dest)
	if err := os.MkdirAll(filepath.Dir(abs), dirMode); err != nil {
		return err
	}
	out, err := os.OpenFile(abs, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode) // #nosec G302 G304 -- path is inside the temporary folder
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("%s collides with another file: %w", label, domain.ErrUnsafePath)
		}
		return err
	}
	n, copyErr := io.Copy(out, io.LimitReader(in, w.limits.MaxFileBytes+1))
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if n > w.limits.MaxFileBytes {
		return fmt.Errorf("%s is larger than %d bytes: %w", label, w.limits.MaxFileBytes, domain.ErrTooLarge)
	}
	w.total += n
	if w.total > w.limits.MaxTotalSize {
		return fmt.Errorf("plugin is larger than %d bytes: %w", w.limits.MaxTotalSize, domain.ErrTooLarge)
	}
	return os.Chmod(abs, mode) // #nosec G302 -- mode is 0644 or 0755 by design
}

// copyLink keeps a symlink only when its target is relative and never climbs:
// no "..", so the link can only point deeper into the plugin.
func (w *writer) copyLink(src fs.FS, p, dest string) error {
	target, err := fs.ReadLink(src, p)
	if err != nil {
		return fmt.Errorf("read link %s: %w", p, err)
	}
	if target == "" || path.IsAbs(target) || filepath.IsAbs(target) || strings.Contains(target, "\\") {
		return fmt.Errorf("symlink %s points to %q: %w", p, target, domain.ErrUnsafePath)
	}
	for _, seg := range strings.Split(target, "/") {
		if seg == ".." {
			return fmt.Errorf("symlink %s points to %q: %w", p, target, domain.ErrUnsafePath)
		}
	}
	abs := w.abs(dest)
	if err := os.MkdirAll(filepath.Dir(abs), dirMode); err != nil {
		return err
	}
	return os.Symlink(filepath.FromSlash(target), abs)
}

func (w *writer) writeFiles(files []domain.File) error {
	for _, f := range files {
		if err := w.ctx.Err(); err != nil {
			return err
		}
		if err := w.countEntry(f.Path); err != nil {
			return err
		}
		if err := w.create(f.Path, strings.NewReader(string(f.Data)), fileMode, f.Path); err != nil {
			return err
		}
	}
	return nil
}

// writeManifest writes the normalised manifest, replacing a manifest of the
// source if the tree had one at the root.
func (w *writer) writeManifest(raw []byte) error {
	if raw == nil {
		return nil
	}
	if err := os.Remove(w.abs(ManifestFile)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return w.create(ManifestFile, strings.NewReader(string(raw)), fileMode, ManifestFile)
}

// TreeDigest is the hex sha256 over a directory: for every file, in sorted
// path order, its slash-separated path, its executable bit and its content.
// A symlink counts with its target. The digest does not depend on the
// creation order, owners or timestamps.
func TreeDigest(dir string) (string, error) {
	var paths []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			paths = append(paths, p)
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("digest %s: %w", dir, err)
	}
	sort.Strings(paths)
	h := sha256.New()
	for _, p := range paths {
		if err := digestOne(h, dir, p); err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

type hashWriter interface{ Write([]byte) (int, error) }

func digestOne(h hashWriter, dir, p string) error {
	rel, err := filepath.Rel(dir, p)
	if err != nil {
		return err
	}
	info, err := os.Lstat(p)
	if err != nil {
		return err
	}
	kind := "f"
	if info.Mode().Perm()&0o111 != 0 {
		kind = "x"
	}
	var content []byte
	switch {
	case info.Mode()&fs.ModeSymlink != 0:
		kind = "l"
		target, err := os.Readlink(p)
		if err != nil {
			return err
		}
		content = []byte(target)
	case info.Mode().IsRegular():
		content, err = os.ReadFile(p) // #nosec G304 -- walking the directory being digested
		if err != nil {
			return err
		}
	default:
		return fmt.Errorf("%s is not a regular file: %w", rel, domain.ErrUnsafePath)
	}
	if _, err := fmt.Fprintf(h, "%s %s %d\n", kind, filepath.ToSlash(rel), len(content)); err != nil {
		return err
	}
	_, err = h.Write(content)
	return err
}
