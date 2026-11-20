// SPDX-License-Identifier: MIT

package fetch

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/SDK-E/agentsone/internal/plugins/domain"
)

const (
	dirMode  = 0o750
	fileMode = 0o644
	execMode = 0o755
)

// fetchLocal copies a directory of this host into the temporary tree. The
// copy applies the size and symlink policy, so a source that points outside
// itself is refused. A .git directory is not copied.
func (f *Fetcher) fetchLocal(spec domain.FetchSpec, dest string) (result, error) {
	info, err := os.Stat(spec.Ref)
	if errors.Is(err, fs.ErrNotExist) {
		return result{}, fmt.Errorf("local source %q does not exist: %w", spec.Ref, domain.ErrNothingToInstall)
	}
	if err != nil {
		return result{}, err
	}
	if !info.IsDir() {
		return result{}, fmt.Errorf("local source %q is not a directory: %w", spec.Ref, domain.ErrNothingToInstall)
	}
	if err := copyDir(spec.Ref, dest, f.limits); err != nil {
		return result{}, err
	}
	return result{root: dest}, nil
}

// copyDir copies src to dest, refusing what the policy refuses.
func copyDir(src, dest string, limits domain.InstallLimits) error {
	if err := os.MkdirAll(dest, dirMode); err != nil {
		return err
	}
	b := &budget{limits: limits}
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(src, p)
		if relErr != nil {
			return relErr
		}
		if rel == "." {
			return nil
		}
		if d.IsDir() && d.Name() == ".git" {
			return filepath.SkipDir
		}
		if err := b.entry(rel); err != nil {
			return err
		}
		out := filepath.Join(dest, rel)
		switch {
		case d.IsDir():
			return os.MkdirAll(out, dirMode)
		case d.Type()&fs.ModeSymlink != 0:
			target, err := os.Readlink(p)
			if err != nil {
				return err
			}
			if err := checkLink(rel, target); err != nil {
				return err
			}
			return os.Symlink(target, out) // #nosec G122 -- target checked by checkLink, destination is our own temporary tree
		case d.Type().IsRegular():
			info, err := d.Info()
			if err != nil {
				return err
			}
			if err := b.bytes(rel, info.Size()); err != nil {
				return err
			}
			return copyFile(p, out, info.Mode().Perm()&0o111 != 0, b.limits.MaxFileBytes)
		}
		return fmt.Errorf("%s is not a regular file: %w", rel, domain.ErrUnsafePath)
	})
}

func copyFile(src, dest string, exec bool, limit int64) error {
	in, err := os.Open(src) // #nosec G304 -- walking the source directory
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }() //nolint:errcheck // read-only source file; the copy reports read failures
	mode := os.FileMode(fileMode)
	if exec {
		mode = execMode
	}
	out, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode) // #nosec G302 G304 -- inside the temporary tree
	if err != nil {
		return err
	}
	n, copyErr := io.Copy(out, io.LimitReader(in, limit+1))
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if n > limit {
		return fmt.Errorf("%s grew while copying: %w", src, domain.ErrTooLarge)
	}
	return os.Chmod(dest, mode) // #nosec G302 -- mode is 0644 or 0755 by design
}
