// SPDX-License-Identifier: MIT

package disk_test

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/SDK-E/agentsone/internal/enginefetch/adapters/disk"
	"github.com/SDK-E/agentsone/internal/enginefetch/app"
	"github.com/SDK-E/agentsone/internal/enginefetch/domain"
	"github.com/SDK-E/agentsone/internal/enginefetch/enginefetchtest"
)

func TestDiskContract(t *testing.T) {
	t.Parallel()
	enginefetchtest.RunDiskContract(t, func(t *testing.T) app.Disk { return disk.New(t.TempDir()) })
}

func TestFakeDiskContract(t *testing.T) {
	t.Parallel()
	enginefetchtest.RunDiskContract(t, func(*testing.T) app.Disk { return enginefetchtest.NewDisk() })
}

func TestInstall_LeavesAnEngineEveryAgentCanRunAtTheBinaryPath(t *testing.T) {
	t.Parallel()
	root := filepath.Join(t.TempDir(), "engines")
	d := disk.New(root)
	p := enginefetchtest.ContractPin
	w, err := d.AppendPart(p, 0)
	if err != nil {
		t.Fatalf("AppendPart() error = %v", err)
	}
	_, _ = io.WriteString(w, "#!/bin/sh\n")
	if err := w.Close(); err != nil {
		t.Fatalf("close part: %v", err)
	}

	if err := d.Install(p); err != nil {
		t.Fatalf("Install() error = %v", err)
	}

	bin := domain.BinaryPath(root, p)
	fi, err := os.Stat(bin)
	if err != nil {
		t.Fatalf("stat %s: %v", bin, err)
	}
	if got := fi.Mode().Perm(); got != 0o755 {
		t.Fatalf("engine mode = %v, want 0755", got)
	}
	for _, dir := range []string{root, filepath.Join(root, p.Engine), filepath.Dir(bin)} {
		fi, err := os.Stat(dir)
		if err != nil || fi.Mode().Perm() != 0o755 {
			t.Fatalf("folder %s mode = %v (%v), want 0755", dir, fi.Mode().Perm(), err)
		}
	}
}

func TestPartSize_ReportsAFolderItCannotRead(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("root reads every folder") // the plain user case is what this proves
	}
	root := t.TempDir()
	p := enginefetchtest.ContractPin
	locked := filepath.Join(root, p.Engine)
	if err := os.Mkdir(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(locked) }) // an empty folder is removable whatever its mode

	if _, err := disk.New(root).PartSize(p); err == nil || os.IsNotExist(err) {
		t.Fatalf("PartSize() error = %v, want a permission error", err)
	}
	if _, err := disk.New(root).AppendPart(p, 0); err == nil {
		t.Fatal("AppendPart() in a locked folder succeeded")
	}
	if err := disk.New(root).RemovePart(p); err == nil {
		t.Fatal("RemovePart() in a locked folder succeeded")
	}
}
