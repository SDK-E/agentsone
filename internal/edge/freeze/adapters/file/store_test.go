// SPDX-License-Identifier: MIT

package file_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/edge/freeze/adapters/file"
	"github.com/SDK-E/agentsone/internal/edge/freeze/domain"
)

func TestStore_LoadsTheZeroStateWhenNothingWasSaved(t *testing.T) {
	t.Parallel()
	got, err := file.New(filepath.Join(t.TempDir(), "freeze.json")).Load(t.Context())
	if err != nil || got != (domain.State{}) {
		t.Fatalf("Load = %+v, %v; want zero, nil", got, err)
	}
}

func TestStore_LoadsWhatItSaved(t *testing.T) {
	t.Parallel()
	s := file.New(filepath.Join(t.TempDir(), "freeze.json"))
	want := domain.State{Frozen: true, Since: time.Date(2026, 11, 19, 21, 0, 0, 0, time.UTC)}
	if err := s.Save(t.Context(), want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := s.Load(t.Context())
	if err != nil || got.Frozen != want.Frozen || !got.Since.Equal(want.Since) {
		t.Fatalf("Load = %+v, %v; want %+v", got, err, want)
	}
}

func TestStore_SavesOwnerOnly(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "freeze.json")
	if err := file.New(path).Save(t.Context(), domain.State{Frozen: true}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, want 0600", fi.Mode().Perm())
	}
}

func TestStore_RefusesAFileItCannotRead(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "freeze.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := file.New(path).Load(t.Context()); err == nil {
		t.Fatal("Load accepted a broken file")
	}
}

func TestStore_SaveFailsWhenTheFolderIsMissing(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "missing", "freeze.json")
	if err := file.New(path).Save(t.Context(), domain.State{Frozen: true}); err == nil {
		t.Fatal("Save into a missing folder succeeded")
	}
}

func TestStore_LoadFailsWhenThePathIsAFolder(t *testing.T) {
	t.Parallel()
	if _, err := file.New(t.TempDir()).Load(t.Context()); err == nil {
		t.Fatal("Load of a folder succeeded")
	}
}
