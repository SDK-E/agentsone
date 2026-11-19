// SPDX-License-Identifier: MIT

package tokenkey_test

import (
	"bytes"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/SDK-E/agentsone/internal/edge/gateway/adapters/tokenkey"
)

func TestLoadOrCreate_MakesAKeyOnlyTheEdgeCanRead(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "gateway-key")

	key, err := tokenkey.LoadOrCreate(path)
	if err != nil {
		t.Fatalf("LoadOrCreate() error = %v", err)
	}

	if len(key) != tokenkey.Size {
		t.Errorf("key is %d bytes, want %d", len(key), tokenkey.Size)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 || !info.Mode().IsRegular() {
		t.Fatalf("key file = %v, %v; want a regular file with mode 0600", info.Mode(), err)
	}
	if bytes.Equal(key, make([]byte, tokenkey.Size)) {
		t.Error("the key is all zeros")
	}
}

func TestLoadOrCreate_KeepsTheKeyAcrossStarts(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "gateway-key")
	first, err := tokenkey.LoadOrCreate(path)
	if err != nil {
		t.Fatalf("first LoadOrCreate() error = %v", err)
	}

	second, err := tokenkey.LoadOrCreate(path)

	if err != nil || !bytes.Equal(first, second) {
		t.Fatalf("second LoadOrCreate() = %x, %v; want the same key %x", second, err, first)
	}
}

func TestLoadOrCreate_TwoStartsAtOnceAgreeOnOneKey(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "gateway-key")
	keys := make([][]byte, 8)
	var wg sync.WaitGroup
	for i := range keys {
		wg.Go(func() {
			k, err := tokenkey.LoadOrCreate(path)
			if err != nil {
				t.Errorf("LoadOrCreate() error = %v", err)
			}
			keys[i] = k
		})
	}
	wg.Wait()

	for _, k := range keys[1:] {
		if !bytes.Equal(k, keys[0]) {
			t.Fatal("two starts made different keys")
		}
	}
}

func TestLoadOrCreate_TightensAKeyFileOthersCouldRead(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "gateway-key")
	if _, err := tokenkey.LoadOrCreate(path); err != nil {
		t.Fatalf("LoadOrCreate() error = %v", err)
	}
	if err := os.Chmod(path, 0o644); err != nil { // #nosec G302 -- the test loosens the mode on purpose
		t.Fatalf("Chmod() error = %v", err)
	}

	if _, err := tokenkey.LoadOrCreate(path); err != nil {
		t.Fatalf("LoadOrCreate() error = %v", err)
	}

	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", info.Mode().Perm())
	}
}

func TestLoadOrCreate_RefusesAKeyFileOfTheWrongSize(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "gateway-key")
	if err := os.WriteFile(path, []byte("short"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	if _, err := tokenkey.LoadOrCreate(path); err == nil {
		t.Fatal("LoadOrCreate() took a broken key file")
	}
	if data, _ := os.ReadFile(path); string(data) != "short" { // #nosec G304 -- test temp folder
		t.Error("the broken file was replaced; a human must look at it")
	}
}

func TestLoadOrCreate_RefusesSomethingThatIsNotAFile(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "gateway-key")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}

	if _, err := tokenkey.LoadOrCreate(path); err == nil {
		t.Fatal("LoadOrCreate() took a folder")
	}
}

func TestLoadOrCreate_ReportsAFolderItCannotWrite(t *testing.T) {
	t.Parallel()
	if _, err := tokenkey.LoadOrCreate(filepath.Join(t.TempDir(), "missing", "gateway-key")); err == nil {
		t.Fatal("LoadOrCreate() in a missing folder succeeded")
	}
}
