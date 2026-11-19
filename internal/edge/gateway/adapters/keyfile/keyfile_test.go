// SPDX-License-Identifier: MIT

package keyfile_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SDK-E/agentsone/internal/edge/gateway/adapters/keyfile"
	gatewayapp "github.com/SDK-E/agentsone/internal/edge/gateway/app"
	routing "github.com/SDK-E/agentsone/internal/edge/gateway/domain"
)

func open(t *testing.T) (*keyfile.Store, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "keys")
	s, err := keyfile.Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return s, dir
}

func TestSetGetHasDelete(t *testing.T) {
	s, dir := open(t)
	ctx := context.Background()
	if err := s.Set(ctx, "openai", "test-key-123"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, err := s.Get(ctx, "openai")
	if err != nil || got != "test-key-123" {
		t.Fatalf("Get = %q, %v", got, err)
	}
	has, err := s.Has(ctx, "openai")
	if err != nil || !has {
		t.Fatalf("Has = %v, %v", has, err)
	}
	info, err := os.Stat(filepath.Join(dir, "openai"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("key file mode = %v, %v", info.Mode(), err)
	}
	dirInfo, err := os.Stat(dir)
	if err != nil || dirInfo.Mode().Perm() != 0o700 {
		t.Fatalf("key folder mode = %v, %v", dirInfo.Mode(), err)
	}
	// Replacing a key keeps one file.
	if err := s.Set(ctx, "openai", "test-key-456"); err != nil {
		t.Fatalf("Set again: %v", err)
	}
	if got, _ := s.Get(ctx, "openai"); got != "test-key-456" {
		t.Fatalf("Get after replace = %q", got)
	}
	if err := s.Delete(ctx, "openai"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := s.Get(ctx, "openai"); !errors.Is(err, gatewayapp.ErrKeyNotFound) {
		t.Fatalf("Get after delete error = %v", err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Fatalf("files left behind: %v", entries)
	}
}

func TestMissingKey(t *testing.T) {
	s, _ := open(t)
	ctx := context.Background()
	if _, err := s.Get(ctx, "groq"); !errors.Is(err, gatewayapp.ErrKeyNotFound) {
		t.Fatalf("Get error = %v", err)
	}
	if has, err := s.Has(ctx, "groq"); err != nil || has {
		t.Fatalf("Has = %v, %v", has, err)
	}
	if err := s.Delete(ctx, "groq"); !errors.Is(err, gatewayapp.ErrKeyNotFound) {
		t.Fatalf("Delete error = %v", err)
	}
}

func TestRefusesUnsafeInput(t *testing.T) {
	s, dir := open(t)
	ctx := context.Background()
	for _, bad := range []string{"", "has space", "new\nline", strings.Repeat("k", 5000)} {
		if err := s.Set(ctx, "openai", bad); !errors.Is(err, keyfile.ErrBadKey) {
			t.Fatalf("Set(%q) error = %v, want ErrBadKey", bad, err)
		}
	}
	for _, bad := range []routing.ProviderID{"", "Open AI", "../etc", "a/b", "."} {
		if err := s.Set(ctx, bad, "k"); !errors.Is(err, keyfile.ErrBadProvider) {
			t.Fatalf("Set provider %q error = %v", bad, err)
		}
		if _, err := s.Get(ctx, bad); !errors.Is(err, keyfile.ErrBadProvider) {
			t.Fatalf("Get provider %q error = %v", bad, err)
		}
		if _, err := s.Has(ctx, bad); !errors.Is(err, keyfile.ErrBadProvider) {
			t.Fatalf("Has provider %q error = %v", bad, err)
		}
		if err := s.Delete(ctx, bad); !errors.Is(err, keyfile.ErrBadProvider) {
			t.Fatalf("Delete provider %q error = %v", bad, err)
		}
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("refused input wrote files: %v", entries)
	}
}

func TestOpenFailsWhenFolderCannotBeMade(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := keyfile.Open(filepath.Join(file, "keys")); err == nil {
		t.Fatal("Open succeeded under a file")
	}
}
