// SPDX-License-Identifier: MIT

package adminsock_test

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SDK-E/agentsone/internal/edge/adminsock"
)

// shortDir keeps socket paths under the 104 byte limit of macOS.
func shortDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "as")
	if err != nil {
		t.Fatalf("MkdirTemp() error = %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func TestListen_MakesASocketOnlyOwnerAndGroupCanUse(t *testing.T) {
	t.Parallel()
	path := filepath.Join(shortDir(t), "run", "admin.sock")

	ln, err := adminsock.Listen(path)
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	defer func() { _ = ln.Close() }()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if info.Mode()&os.ModeSocket == 0 || info.Mode().Perm() != 0o660 {
		t.Errorf("mode = %v, want a socket with 0660", info.Mode())
	}
	c, err := net.Dial("unix", path)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	_ = c.Close()
}

func TestListen_ReplacesASocketLeftByAnEarlierRun(t *testing.T) {
	t.Parallel()
	path := filepath.Join(shortDir(t), "admin.sock")
	old, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	old.(*net.UnixListener).SetUnlinkOnClose(false)
	_ = old.Close()

	ln, err := adminsock.Listen(path)
	if err != nil {
		t.Fatalf("Listen() over a stale socket error = %v", err)
	}
	_ = ln.Close()
}

func TestListen_RefusesToReplaceSomethingThatIsNotASocket(t *testing.T) {
	t.Parallel()
	path := filepath.Join(shortDir(t), "admin.sock")
	if err := os.WriteFile(path, []byte("keep me"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	if ln, err := adminsock.Listen(path); err == nil {
		_ = ln.Close()
		t.Fatal("Listen() replaced a regular file")
	}
	if data, _ := os.ReadFile(path); string(data) != "keep me" { // #nosec G304 -- test temp folder
		t.Error("the file was changed")
	}
}

func TestListen_ReportsAFolderThatCannotBeMade(t *testing.T) {
	t.Parallel()
	file := filepath.Join(shortDir(t), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	if ln, err := adminsock.Listen(filepath.Join(file, "admin.sock")); err == nil {
		_ = ln.Close()
		t.Fatal("Listen() under a file succeeded")
	}
}

func TestListen_ReportsAPathTooLongForASocket(t *testing.T) {
	t.Parallel()
	long := filepath.Join(shortDir(t), strings.Repeat("a", 120))

	if ln, err := adminsock.Listen(long); err == nil {
		_ = ln.Close()
		t.Fatal("Listen() on a 120 byte name succeeded")
	}
}

func TestListen_RemovesTheSocketOnClose(t *testing.T) {
	t.Parallel()
	path := filepath.Join(shortDir(t), "admin.sock")
	ln, err := adminsock.Listen(path)
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	_ = ln.Close()

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("socket still there after Close: %v", err)
	}
}
