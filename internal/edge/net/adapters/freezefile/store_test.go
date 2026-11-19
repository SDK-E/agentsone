// SPDX-License-Identifier: MIT

package freezefile_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/SDK-E/agentsone/internal/edge/net/adapters/freezefile"
	"github.com/SDK-E/agentsone/internal/edge/net/app"
	"github.com/SDK-E/agentsone/internal/edge/net/nettest"
)

func TestFileFreezeStore(t *testing.T) {
	t.Parallel()
	nettest.RunFreezeStoreContract(t, func(t *testing.T) app.FreezeStore {
		return freezefile.New(filepath.Join(t.TempDir(), "data", "egress-frozen"))
	})
}

func TestFileFreezeStore_SurvivesANewStoreOnTheSameFile(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "egress-frozen")
	if err := freezefile.New(path).Save(t.Context(), true); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	got, err := freezefile.New(path).Load(t.Context())

	if err != nil || !got {
		t.Fatalf("Load() = %v, %v; want true", got, err)
	}
}

func TestFileFreezeStore_WritesAFileOnlyTheOwnerCanRead(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "egress-frozen")
	if err := freezefile.New(path).Save(t.Context(), true); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("Stat() = %v, %v; want mode 0600", info, err)
	}
}

func TestFileFreezeStore_ReportsAnUnreadableSwitch(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "egress-frozen")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}

	if _, err := freezefile.New(path).Load(t.Context()); err == nil {
		t.Fatal("Load() of a folder in place of the switch succeeded")
	}
}
