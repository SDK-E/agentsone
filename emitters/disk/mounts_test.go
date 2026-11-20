// SPDX-License-Identifier: MIT

package disk_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SDK-E/agentsone/emitters/disk"
)

const procMounts = `overlay / overlay rw,relatime,lowerdir=/a,upperdir=/b 0 0
proc /proc proc rw,nosuid 0 0
/dev/vda1 /var/lib/my\040data ext4 rw,relatime 0 0

garbage line
tmpfs /run tmpfs rw 0 0
`

func TestParseMounts(t *testing.T) {
	t.Parallel()
	got := disk.ParseMounts(strings.NewReader(procMounts))
	want := []disk.Mount{
		{Path: "/", FSType: "overlay"},
		{Path: "/proc", FSType: "proc"},
		{Path: "/var/lib/my data", FSType: "ext4"},
		{Path: "/run", FSType: "tmpfs"},
	}
	if len(got) != len(want) {
		t.Fatalf("mounts = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("mount %d = %v, want %v", i, got[i], want[i])
		}
	}
}

func TestMountsFile_ListsFromAFile(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "mounts")
	if err := os.WriteFile(path, []byte(procMounts), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := disk.MountsFile(path).List()
	if err != nil || len(got) != 4 {
		t.Fatalf("List = %v, %v; want 4 mounts", got, err)
	}
}

func TestMountsFile_MissingFileIsAnError(t *testing.T) {
	t.Parallel()
	if _, err := disk.MountsFile(filepath.Join(t.TempDir(), "none")).List(); err == nil {
		t.Fatal("List error = nil, want one")
	}
}

func TestStatFS_ReadsARealFileSystem(t *testing.T) {
	t.Parallel()
	u, err := disk.StatFS{}.Usage(t.TempDir())
	if err != nil {
		t.Fatalf("Usage error = %v", err)
	}
	if u.TotalBytes == 0 || u.UsedBytes > u.TotalBytes {
		t.Fatalf("usage = %+v, want a sane non-empty file system", u)
	}
}

func TestStatFS_MissingPathIsAnError(t *testing.T) {
	t.Parallel()
	if _, err := (disk.StatFS{}).Usage(filepath.Join(t.TempDir(), "none")); err == nil {
		t.Fatal("Usage error = nil, want one")
	}
}

func TestUsage_Percent(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		u    disk.Usage
		want int
	}{
		"empty":               {disk.Usage{}, 0},
		"half":                {disk.Usage{TotalBytes: 200, UsedBytes: 50, AvailBytes: 50}, 50},
		"rounds up like df":   {disk.Usage{TotalBytes: 1000, UsedBytes: 901, AvailBytes: 99}, 91},
		"reserved blocks":     {disk.Usage{TotalBytes: 1000, UsedBytes: 900, AvailBytes: 50}, 95},
		"full":                {disk.Usage{TotalBytes: 100, UsedBytes: 100, AvailBytes: 0}, 100},
		"no space and unused": {disk.Usage{TotalBytes: 100, UsedBytes: 0, AvailBytes: 0}, 0},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := tc.u.Percent(); got != tc.want {
				t.Fatalf("Percent() = %d, want %d", got, tc.want)
			}
		})
	}
}
