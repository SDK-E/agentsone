// SPDX-License-Identifier: MIT

// Package system implements the machine ports on the real container: files,
// /proc, /etc/passwd, apt and the recipe store on the state volume.
package system

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/user"
	"path"
	"strconv"
	"strings"
	"syscall"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/machine/app"
	"github.com/SDK-E/agentsone/internal/machine/domain"
)

// Files works inside an area through os.Root, so a symlink or ".." can never
// lead outside the area's base folder.
type Files struct{}

var _ app.FileSystem = Files{}

// NewFiles returns the file system adapter.
func NewFiles() Files { return Files{} }

func openRoot(a domain.Area) (*os.Root, error) {
	r, err := os.OpenRoot(a.Base)
	if err != nil {
		return nil, mapErr(a.Base, err)
	}
	return r, nil
}

// List lists a folder.
func (Files) List(_ context.Context, a domain.Area) ([]domain.FileEntry, error) {
	r, err := openRoot(a)
	if err != nil {
		return nil, err
	}
	defer func() { _ = r.Close() }()
	info, err := r.Stat(a.Rel)
	if err != nil {
		return nil, mapErr(a.Path, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a folder: %w", a.Path, core.ErrInvalid)
	}
	dir, err := r.Open(a.Rel)
	if err != nil {
		return nil, mapErr(a.Path, err)
	}
	defer func() { _ = dir.Close() }()
	names, err := dir.Readdirnames(-1)
	if err != nil {
		return nil, mapErr(a.Path, err)
	}
	out := make([]domain.FileEntry, 0, len(names))
	for _, name := range names {
		fi, err := r.Lstat(path.Join(a.Rel, name))
		if err != nil {
			continue // removed while listing
		}
		out = append(out, toEntry(path.Join(a.Path, name), fi))
	}
	return out, nil
}

// Read returns at most limit bytes and the full size.
func (Files) Read(_ context.Context, a domain.Area, limit int64) ([]byte, int64, error) {
	r, err := openRoot(a)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = r.Close() }()
	f, err := r.Open(a.Rel)
	if err != nil {
		return nil, 0, mapErr(a.Path, err)
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return nil, 0, mapErr(a.Path, err)
	}
	if info.IsDir() {
		return nil, 0, fmt.Errorf("%s is a folder: %w", a.Path, core.ErrInvalid)
	}
	data, err := io.ReadAll(io.LimitReader(f, limit))
	if err != nil {
		return nil, 0, mapErr(a.Path, err)
	}
	return data, info.Size(), nil
}

// Write replaces the file atomically: it writes a temporary file next to it,
// sets owner and mode, then renames it over the old one. A symlink at the
// path is replaced, never followed.
func (Files) Write(_ context.Context, a domain.Area, w app.FileWrite) (domain.FileEntry, error) {
	r, err := openRoot(a)
	if err != nil {
		return domain.FileEntry{}, err
	}
	defer func() { _ = r.Close() }()
	dir := path.Dir(a.Rel)
	if w.CreateParents {
		if err := r.MkdirAll(dir, 0o755); err != nil {
			return domain.FileEntry{}, mapErr(a.Path, err)
		}
	}
	tmp := path.Join(dir, "."+path.Base(a.Rel)+".agentsone-tmp")
	if err := writeTemp(r, tmp, w); err != nil {
		_ = r.Remove(tmp)
		return domain.FileEntry{}, mapErr(a.Path, err)
	}
	if err := r.Rename(tmp, a.Rel); err != nil {
		_ = r.Remove(tmp)
		return domain.FileEntry{}, mapErr(a.Path, err)
	}
	info, err := r.Lstat(a.Rel)
	if err != nil {
		return domain.FileEntry{}, mapErr(a.Path, err)
	}
	return toEntry(a.Path, info), nil
}

func writeTemp(r *os.Root, name string, w app.FileWrite) error {
	f, err := r.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(w.Data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := r.Lchown(name, w.UID, w.GID); err != nil {
		return err
	}
	return r.Chmod(name, w.Mode) // after chown, which may clear setuid bits
}

// Delete removes a file, or a folder when recursive.
func (Files) Delete(_ context.Context, a domain.Area, recursive bool) error {
	r, err := openRoot(a)
	if err != nil {
		return err
	}
	defer func() { _ = r.Close() }()
	info, err := r.Lstat(a.Rel)
	if err != nil {
		return mapErr(a.Path, err)
	}
	if info.IsDir() && recursive {
		return mapErr(a.Path, r.RemoveAll(a.Rel))
	}
	if err := r.Remove(a.Rel); err != nil {
		if info.IsDir() {
			return fmt.Errorf("%s is a folder that is not empty: %w", a.Path, core.ErrConflict)
		}
		return mapErr(a.Path, err)
	}
	return nil
}

func toEntry(p string, fi fs.FileInfo) domain.FileEntry {
	e := domain.FileEntry{Path: p, Name: path.Base(p), Size: fi.Size(), Mode: fi.Mode(), Modified: fi.ModTime().UTC()}
	switch {
	case fi.Mode().IsRegular():
		e.Type = domain.FileRegular
	case fi.IsDir():
		e.Type = domain.FileDir
	case fi.Mode()&fs.ModeSymlink != 0:
		e.Type = domain.FileSymlink
	default:
		e.Type = domain.FileOther
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		e.Owner = userName(int(st.Uid))
		e.Group = groupName(int(st.Gid))
	}
	return e
}

func userName(uid int) string {
	if u, err := user.LookupId(strconv.Itoa(uid)); err == nil {
		return u.Username
	}
	return strconv.Itoa(uid)
}

func groupName(gid int) string {
	if g, err := user.LookupGroupId(strconv.Itoa(gid)); err == nil {
		return g.Name
	}
	return strconv.Itoa(gid)
}

// mapErr turns file system errors into the daemon's sentinels. os.Root
// reports an escape through a symlink or ".." with an unexported error, so
// its text is matched.
func mapErr(p string, err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("%s: %w", p, core.ErrNotFound)
	case errors.Is(err, fs.ErrPermission):
		return fmt.Errorf("%s: %w", p, core.ErrForbidden)
	case strings.Contains(err.Error(), "escapes from parent"):
		return fmt.Errorf("%s leads outside the allowed folder: %w", p, core.ErrForbidden)
	}
	return fmt.Errorf("%s: %w", p, err)
}
