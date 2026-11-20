// SPDX-License-Identifier: MIT

package files_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/SDK-E/agentsone/emitters/files"
)

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestDisk_WalkListsRegularFilesBelowTheWatch(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	write(t, filepath.Join(dir, "a.txt"), "hello")
	write(t, filepath.Join(dir, "sub", "b.txt"), "hi")
	if err := os.Symlink(filepath.Join(dir, "a.txt"), filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}

	got, err := files.Disk{}.Walk(dir, 100)

	if err != nil {
		t.Fatalf("Walk error = %v", err)
	}
	if len(got) != 2 || got[filepath.Join(dir, "a.txt")].Size != 5 || got[filepath.Join(dir, "sub", "b.txt")].Size != 2 {
		t.Fatalf("Walk = %+v, want the two regular files and not the link", got)
	}
}

func TestDisk_WalkRefusesATreeOverTheLimit(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for _, n := range []string{"a", "b", "c"} {
		write(t, filepath.Join(dir, n), n)
	}

	_, err := files.Disk{}.Walk(dir, 2)

	if !errors.Is(err, files.ErrTooManyFiles) {
		t.Fatalf("Walk error = %v, want ErrTooManyFiles", err)
	}
}

func TestDisk_WalkOfAMissingWatchIsAnError(t *testing.T) {
	t.Parallel()
	if _, err := (files.Disk{}).Walk(filepath.Join(t.TempDir(), "gone"), 10); err == nil {
		t.Fatal("Walk error = nil, want one for a watch that does not exist")
	}
}
