// SPDX-License-Identifier: MIT

package fetch_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/SDK-E/agentsone/internal/plugins/adapters/fetch"
	"github.com/SDK-E/agentsone/internal/plugins/domain"
)

func TestFetch_LocalCopiesTheDirectoryWithoutGit(t *testing.T) {
	t.Parallel()
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "skills/a/SKILL.md"), "skill", 0o644)
	writeFile(t, filepath.Join(src, "bin/run"), "#!/bin/sh", 0o755)
	writeFile(t, filepath.Join(src, ".git/config"), "secret", 0o644)
	f, tmp := newFetcher(t)

	tree, err := f.Fetch(context.Background(), domain.FetchSpec{Kind: domain.FetchLocal, Ref: src})
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}

	if readTree(t, tree, "skills/a/SKILL.md") != "skill" {
		t.Error("file not copied")
	}
	if _, err := os.Stat(filepath.Join(tree.Dir, ".git")); !errors.Is(err, os.ErrNotExist) {
		t.Error(".git was copied")
	}
	if info, err := os.Stat(filepath.Join(tree.Dir, "bin/run")); err != nil || info.Mode().Perm()&0o100 == 0 {
		t.Errorf("exec bit lost: %v %v", info, err)
	}
	if tree.Dir == src {
		t.Error("tree is the source itself, not a copy")
	}
	tree.Cleanup()
	assertNothingLeft(t, tmp)
	if _, err := os.Stat(src); err != nil {
		t.Errorf("Cleanup removed the source: %v", err)
	}
}

func TestFetch_LocalSubdir(t *testing.T) {
	t.Parallel()
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "plugins/one/x.txt"), "1", 0o644)
	f, _ := newFetcher(t)

	tree, err := f.Fetch(context.Background(), domain.FetchSpec{Kind: domain.FetchLocal, Ref: src, Subdir: "plugins/one"})
	if err != nil {
		t.Fatal(err)
	}
	defer tree.Cleanup()

	if readTree(t, tree, "x.txt") != "1" {
		t.Error("subdir not applied")
	}
}

func TestFetch_SubdirThatLeavesTheSourceIsRefused(t *testing.T) {
	t.Parallel()
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "ok/x.txt"), "1", 0o644)
	writeFile(t, filepath.Join(src, "file.txt"), "1", 0o644)
	for subdir, want := range map[string]error{
		"..":          domain.ErrUnsafePath,
		"../x":        domain.ErrUnsafePath,
		"ok/../../x":  domain.ErrUnsafePath,
		"/etc":        domain.ErrUnsafePath,
		".":           domain.ErrUnsafePath,
		`ok\..\..`:    domain.ErrUnsafePath,
		"missing":     domain.ErrNothingToInstall,
		"file.txt":    domain.ErrNothingToInstall,
		"ok/./../ok2": domain.ErrUnsafePath,
	} {
		f, tmp := newFetcher(t)
		_, err := f.Fetch(context.Background(), domain.FetchSpec{Kind: domain.FetchLocal, Ref: src, Subdir: subdir})
		if !errors.Is(err, want) {
			t.Errorf("Subdir %q: error = %v, want %v", subdir, err, want)
		}
		assertNothingLeft(t, tmp)
	}
}

func TestFetch_LocalSymlinksThatLeaveTheSourceAreRefused(t *testing.T) {
	t.Parallel()
	for name, target := range map[string]string{"up": "../outside", "absolute": "/etc/passwd", "hidden up": "a/../../x"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			src := t.TempDir()
			writeFile(t, filepath.Join(src, "ok.txt"), "1", 0o644)
			if err := os.Symlink(target, filepath.Join(src, "link")); err != nil {
				t.Fatal(err)
			}
			f, tmp := newFetcher(t)

			_, err := f.Fetch(context.Background(), domain.FetchSpec{Kind: domain.FetchLocal, Ref: src})

			if !errors.Is(err, domain.ErrUnsafePath) {
				t.Fatalf("error = %v, want ErrUnsafePath", err)
			}
			assertNothingLeft(t, tmp)
		})
	}
}

func TestFetch_LocalSymlinkInsideTheSourceIsKept(t *testing.T) {
	t.Parallel()
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "real/file.txt"), "data", 0o644)
	if err := os.Symlink("real/file.txt", filepath.Join(src, "link.txt")); err != nil {
		t.Fatal(err)
	}
	f, _ := newFetcher(t)

	tree, err := f.Fetch(context.Background(), domain.FetchSpec{Kind: domain.FetchLocal, Ref: src})
	if err != nil {
		t.Fatal(err)
	}
	defer tree.Cleanup()

	if readTree(t, tree, "link.txt") != "data" {
		t.Error("link does not resolve")
	}
}

func TestFetch_LocalSizeLimitsAndMissingSources(t *testing.T) {
	t.Parallel()
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "a"), "12345", 0o644)
	writeFile(t, filepath.Join(src, "b"), "12345", 0o644)
	file := filepath.Join(src, "a")

	tests := []struct {
		name   string
		ref    string
		limits domain.InstallLimits
		want   error
	}{
		{"file too big", src, domain.InstallLimits{MaxFiles: 10, MaxFileBytes: 4, MaxTotalSize: 100}, domain.ErrTooLarge},
		{"total too big", src, domain.InstallLimits{MaxFiles: 10, MaxFileBytes: 10, MaxTotalSize: 8}, domain.ErrTooLarge},
		{"too many files", src, domain.InstallLimits{MaxFiles: 1, MaxFileBytes: 10, MaxTotalSize: 100}, domain.ErrTooLarge},
		{"missing", filepath.Join(src, "nope"), domain.DefaultInstallLimits, domain.ErrNothingToInstall},
		{"not a directory", file, domain.DefaultInstallLimits, domain.ErrNothingToInstall},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f, tmp := newFetcher(t, fetch.WithLimits(tt.limits))
			_, err := f.Fetch(context.Background(), domain.FetchSpec{Kind: domain.FetchLocal, Ref: tt.ref})
			if !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want %v", err, tt.want)
			}
			assertNothingLeft(t, tmp)
		})
	}
}

func TestFetch_RefusesUnknownKindsAndEmptyRefs(t *testing.T) {
	t.Parallel()
	f, tmp := newFetcher(t)
	for _, spec := range []domain.FetchSpec{{Kind: "ftp", Ref: "x"}, {Kind: domain.FetchLocal}, {Kind: ""}} {
		if _, err := f.Fetch(context.Background(), spec); !errors.Is(err, domain.ErrUnsupportedSource) {
			t.Errorf("Fetch(%+v) error = %v, want ErrUnsupportedSource", spec, err)
		}
	}
	assertNothingLeft(t, tmp)
}
