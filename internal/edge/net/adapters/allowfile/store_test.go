// SPDX-License-Identifier: MIT

package allowfile_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/edge/net/adapters/allowfile"
	"github.com/SDK-E/agentsone/internal/edge/net/app"
	"github.com/SDK-E/agentsone/internal/edge/net/domain"
	"github.com/SDK-E/agentsone/internal/edge/net/nettest"
)

func TestFileStore(t *testing.T) {
	t.Parallel()
	nettest.RunAllowListStoreContract(t, func(t *testing.T) app.AllowListStore {
		return allowfile.New(filepath.Join(t.TempDir(), "allowlist.toml"))
	})
}

func TestFileStore_WritesFilesOnlyTheOwnerCanRead(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "data", "allowlist.toml")
	s := allowfile.New(path)

	if err := s.Init(t.Context()); err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, want 0600", info.Mode().Perm())
	}
}

func TestFileStore_DefaultFileExplainsEveryEntry(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "allowlist.toml")
	if err := allowfile.New(path).Init(t.Context()); err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	data, err := os.ReadFile(path) // #nosec G304 -- path is in the test's temp folder
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	for _, e := range domain.DefaultAllowList().Entries() {
		if !strings.Contains(string(data), e.Why) {
			t.Errorf("file lacks the reason for %s", e.Pattern)
		}
	}
}

func TestFileStore_LoadReadsAHandWrittenFile(t *testing.T) {
	t.Parallel()
	path := writeFile(t, `# my list
[[allow]]
pattern = "Example.org"
why = "docs"

[[allow]]
pattern = "*.example.net"
`)

	got, err := allowfile.New(path).Load(t.Context())
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	entries := got.Entries()
	if len(entries) != 2 || entries[0].Pattern.String() != "*.example.net" || entries[1].Why != "docs" {
		t.Fatalf("Load() = %+v, want the two hand-written entries", entries)
	}
}

func TestFileStore_LoadRefusesBrokenFiles(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		contents string
		wantText string
	}{
		{"not toml", "[[allow]\npattern = 1", "line"},
		{"bad pattern", "[[allow]]\npattern = \"*.com\"\n", "*.com"},
		{"unknown key", "[[allow]]\npattern = \"example.org\"\nreason = \"typo\"\n", "reason"},
		{"duplicate entry", "[[allow]]\npattern = \"example.org\"\n[[allow]]\npattern = \"example.org\"\n", "already present"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			path := writeFile(t, tt.contents)

			_, err := allowfile.New(path).Load(t.Context())

			if err == nil || !strings.Contains(err.Error(), tt.wantText) {
				t.Fatalf("Load() error = %v, want one mentioning %q", err, tt.wantText)
			}
		})
	}
}

func TestFileStore_LoadAcceptsAnEmptyList(t *testing.T) {
	t.Parallel()
	path := writeFile(t, "# nothing allowed\n")

	got, err := allowfile.New(path).Load(t.Context())

	if err != nil || len(got.Entries()) != 0 {
		t.Fatalf("Load() = %d entries, %v; want an empty list", len(got.Entries()), err)
	}
}

func TestWatcher_ReportsAChangeToTheFile(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "allowlist.toml")
	store := allowfile.New(path)
	if err := store.Init(t.Context()); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	changed := make(chan struct{}, 16)
	go func() { _ = allowfile.NewWatcher(path).Watch(t.Context(), func() { changed <- struct{}{} }) }()
	waitFor(t, changed, "the report the watcher makes when it is in place")

	if _, err := store.Update(t.Context(), func(l domain.AllowList) (domain.AllowList, error) {
		p, _ := domain.ParsePattern("example.org")
		return l.With(domain.Entry{Pattern: p})
	}); err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	waitFor(t, changed, "change reported")
}

func TestWatcher_ReportsAFileWrittenByHand(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "allowlist.toml")
	changed := make(chan struct{}, 16)
	go func() { _ = allowfile.NewWatcher(path).Watch(t.Context(), func() { changed <- struct{}{} }) }()
	waitFor(t, changed, "the report the watcher makes when it is in place")

	if err := os.WriteFile(path, []byte("# empty\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	waitFor(t, changed, "change reported")
}

func TestWatcher_ReportsOnceWhenInPlaceSoAnEarlierChangeIsNotMissed(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "allowlist.toml")
	changed := make(chan struct{}, 16)

	go func() { _ = allowfile.NewWatcher(path).Watch(t.Context(), func() { changed <- struct{}{} }) }()

	waitFor(t, changed, "the report the watcher makes when it is in place")
}

func TestWatcher_StopsWhenTheContextEnds(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "allowlist.toml")
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- allowfile.NewWatcher(path).Watch(ctx, func() {}) }()

	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Watch() error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Watch did not return within 5s of cancel")
	}
}

func writeFile(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "allowlist.toml")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	return path
}

func waitFor[T any](t *testing.T, ch <-chan T, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}
