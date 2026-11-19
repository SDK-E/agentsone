// SPDX-License-Identifier: MIT

package domain

import (
	"fmt"
	"io/fs"
	"path"
	"strconv"
	"strings"
	"time"
)

// MaxFileBytes caps one file read or write (8 MiB, the contract's limit).
const MaxFileBytes = 8 << 20

// SharedDir is the folder every agent may use to hand files to others.
const SharedDir = "/home/shared"

// tmpDir is scratch space every agent may use; it resets with the machine.
const tmpDir = "/tmp"

// persistentRoots live on volumes and survive a restart, so the recipe
// never needs to restore them. Ephemeral roots are kernel or runtime views
// that must never be restored.
var (
	persistentRoots = []string{"/home", "/var/lib/agentsone"}
	ephemeralRoots  = []string{"/tmp", "/run", "/proc", "/sys", "/dev"}
)

// FileType is what a folder entry is.
type FileType string

// File types.
const (
	FileRegular FileType = "file"
	FileDir     FileType = "directory"
	FileSymlink FileType = "symlink"
	FileOther   FileType = "other"
)

// FileEntry is one folder entry.
type FileEntry struct {
	Path     string
	Name     string
	Type     FileType
	Size     int64
	Mode     fs.FileMode
	Owner    string
	Group    string
	Modified time.Time
}

// Area is where a file operation may act: an allowed base folder and the
// path relative to it. Adapters open Base as a root so symlinks cannot leave
// it.
type Area struct {
	Path string
	Base string
	Rel  string
}

// ParsePath checks that p is absolute and NUL-free and returns it cleaned.
func ParsePath(p string) (string, error) {
	if !strings.HasPrefix(p, "/") {
		return "", invalid("path %q is not absolute", p)
	}
	if strings.ContainsRune(p, 0) {
		return "", invalid("path holds a NUL byte")
	}
	return path.Clean(p), nil
}

// AreaFor decides where actor a may act for path p. Privileged actors act
// anywhere; custom agents only under their home, the shared folder and /tmp.
func AreaFor(a Actor, p string) (Area, error) {
	clean, err := ParsePath(p)
	if err != nil {
		return Area{}, err
	}
	if a.Privileged() {
		return area(clean, "/"), nil
	}
	for _, base := range allowedBases(a) {
		if within(clean, base) {
			return area(clean, base), nil
		}
	}
	return Area{}, forbidden("%s may not touch %s", a.Label(), clean)
}

func allowedBases(a Actor) []string {
	bases := []string{SharedDir, tmpDir}
	if a.Home != "" {
		bases = append([]string{path.Clean(a.Home)}, bases...)
	}
	return bases
}

func area(clean, base string) Area {
	rel := strings.TrimPrefix(strings.TrimPrefix(clean, base), "/")
	if rel == "" {
		rel = "."
	}
	return Area{Path: clean, Base: base, Rel: rel}
}

func within(p, base string) bool {
	return p == base || strings.HasPrefix(p, base+"/")
}

// RecipeWorthy reports whether a file written at p is lost on restart and
// so belongs in the machine recipe.
func RecipeWorthy(p string) bool {
	for _, root := range append(append([]string{}, persistentRoots...), ephemeralRoots...) {
		if within(p, root) {
			return false
		}
	}
	return true
}

// ParseMode reads a four-digit octal mode such as "0644".
func ParseMode(s string) (fs.FileMode, error) {
	if len(s) != 4 {
		return 0, invalid("mode %q is not four octal digits", s)
	}
	n, err := strconv.ParseUint(s, 8, 32)
	if err != nil {
		return 0, invalid("mode %q is not octal", s)
	}
	return fs.FileMode(n), nil
}

// FormatMode writes the permission and special bits as four octal digits.
func FormatMode(m fs.FileMode) string {
	bits := uint32(m.Perm())
	if m&fs.ModeSetuid != 0 {
		bits |= 0o4000
	}
	if m&fs.ModeSetgid != 0 {
		bits |= 0o2000
	}
	if m&fs.ModeSticky != 0 {
		bits |= 0o1000
	}
	return fmt.Sprintf("%04o", bits)
}

// CheckFileSize refuses bodies over MaxFileBytes.
func CheckFileSize(n int64) error {
	if n > MaxFileBytes {
		return fmt.Errorf("%d bytes is over the %d byte cap: %w", n, MaxFileBytes, ErrTooLarge)
	}
	return nil
}
