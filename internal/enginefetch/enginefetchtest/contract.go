// SPDX-License-Identifier: MIT

package enginefetchtest

import (
	"errors"
	"io"
	"io/fs"
	"testing"

	"github.com/SDK-E/agentsone/internal/enginefetch/app"
	"github.com/SDK-E/agentsone/internal/enginefetch/domain"
)

// ContractPin is the pin the disk contract suite stores files for.
var ContractPin = domain.Pin{Engine: "claude-code", Version: "1.2.3"}

// RunDiskContract checks the behaviour every app.Disk must have (TESTING.md
// B3b). It runs against the real disk and against the fake.
func RunDiskContract(t *testing.T, newDisk func(t *testing.T) app.Disk) {
	t.Helper()
	p := ContractPin
	write := func(t *testing.T, d app.Disk, offset int64, b string) {
		t.Helper()
		w, err := d.AppendPart(p, offset)
		if err != nil {
			t.Fatalf("AppendPart(%d) error = %v", offset, err)
		}
		if _, err := io.WriteString(w, b); err != nil {
			t.Fatalf("write part: %v", err)
		}
		if err := w.Close(); err != nil {
			t.Fatalf("close part: %v", err)
		}
	}

	t.Run("an empty disk has nothing installed and no part", func(t *testing.T) {
		t.Parallel()
		d := newDisk(t)
		if _, _, err := d.Installed(p); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("Installed() error = %v, want %v", err, fs.ErrNotExist)
		}
		if n, err := d.PartSize(p); n != 0 || err != nil {
			t.Fatalf("PartSize() = %d, %v, want 0, nil", n, err)
		}
		if _, _, err := d.PartSum(p); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("PartSum() error = %v, want %v", err, fs.ErrNotExist)
		}
	})

	t.Run("appending grows the part and its sum covers every byte", func(t *testing.T) {
		t.Parallel()
		d := newDisk(t)
		write(t, d, 0, "hello ")
		write(t, d, 6, "world")
		sum, n, err := d.PartSum(p)
		if err != nil || n != 11 || sum != Sum([]byte("hello world")) {
			t.Fatalf("PartSum() = %s, %d, %v, want the sum of 11 bytes", sum, n, err)
		}
	})

	t.Run("appending at an earlier offset drops the bytes after it", func(t *testing.T) {
		t.Parallel()
		d := newDisk(t)
		write(t, d, 0, "hello world")
		write(t, d, 5, "!")
		if n, _ := d.PartSize(p); n != 6 {
			t.Fatalf("PartSize() = %d, want 6", n)
		}
	})

	t.Run("install moves the part into place", func(t *testing.T) {
		t.Parallel()
		d := newDisk(t)
		write(t, d, 0, "engine")
		if err := d.Install(p); err != nil {
			t.Fatalf("Install() error = %v", err)
		}
		sum, n, err := d.Installed(p)
		if err != nil || n != 6 || sum != Sum([]byte("engine")) {
			t.Fatalf("Installed() = %s, %d, %v, want the engine", sum, n, err)
		}
		if n, _ := d.PartSize(p); n != 0 {
			t.Fatalf("PartSize() after install = %d, want 0", n)
		}
	})

	t.Run("install without a part fails", func(t *testing.T) {
		t.Parallel()
		if err := newDisk(t).Install(p); err == nil {
			t.Fatal("Install() without a part succeeded")
		}
	})

	t.Run("removing what is not there is not an error", func(t *testing.T) {
		t.Parallel()
		d := newDisk(t)
		if err := d.RemovePart(p); err != nil {
			t.Fatalf("RemovePart() error = %v", err)
		}
		if err := d.RemoveInstalled(p); err != nil {
			t.Fatalf("RemoveInstalled() error = %v", err)
		}
	})

	t.Run("remove deletes the part and the installed engine", func(t *testing.T) {
		t.Parallel()
		d := newDisk(t)
		write(t, d, 0, "engine")
		if err := d.Install(p); err != nil {
			t.Fatalf("Install() error = %v", err)
		}
		write(t, d, 0, "next")
		if err := d.RemovePart(p); err != nil {
			t.Fatalf("RemovePart() error = %v", err)
		}
		if err := d.RemoveInstalled(p); err != nil {
			t.Fatalf("RemoveInstalled() error = %v", err)
		}
		if _, _, err := d.Installed(p); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("Installed() after remove error = %v", err)
		}
		if n, _ := d.PartSize(p); n != 0 {
			t.Fatalf("PartSize() after remove = %d", n)
		}
	})
}
