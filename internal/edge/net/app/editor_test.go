// SPDX-License-Identifier: MIT

package app_test

import (
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/SDK-E/agentsone/internal/edge/net/app"
	"github.com/SDK-E/agentsone/internal/edge/net/domain"
	"github.com/SDK-E/agentsone/internal/edge/net/nettest"
)

func TestEditor_ListShowsTheDefaultListBeforeFirstStart(t *testing.T) {
	t.Parallel()
	e := app.NewEditor(nettest.NewStore())

	got, err := e.List(t.Context())
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}

	if len(got) != len(domain.DefaultAllowList().Entries()) {
		t.Fatalf("List() has %d entries, want the %d defaults", len(got), len(domain.DefaultAllowList().Entries()))
	}
}

func TestEditor_AddSavesTheEntry(t *testing.T) {
	t.Parallel()
	store := nettest.NewStore()
	saveList(t, store, "github.com")
	e := app.NewEditor(store)

	if _, err := e.Add(t.Context(), "*.example.org", "testing"); err != nil {
		t.Fatalf("Add() error = %v", err)
	}

	saved, err := store.Load(t.Context())
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if diff := cmp.Diff([]string{"*.example.org", "github.com"}, entryPatterns(saved)); diff != "" {
		t.Fatalf("saved list mismatch (-want +got):\n%s", diff)
	}
}

func TestEditor_AddKeepsTheReason(t *testing.T) {
	t.Parallel()
	e := app.NewEditor(nettest.NewStore())

	got, err := e.Add(t.Context(), "example.org", "  a reason  ")
	if err != nil {
		t.Fatalf("Add() error = %v", err)
	}

	if got.Why != "a reason" || got.Pattern.String() != "example.org" {
		t.Fatalf("Add() = %q %q, want example.org with the trimmed reason", got.Pattern, got.Why)
	}
}

func TestEditor_AddRefusesBadInput(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		pattern string
		why     string
		wantErr error
	}{
		{"bad pattern", "*.com", "", domain.ErrInvalidPattern},
		{"duplicate", "github.com", "", domain.ErrDuplicateEntry},
		{"reason with a new line", "example.org", "one\ntwo", app.ErrInvalidReason},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			store := nettest.NewStore()
			saveList(t, store, "github.com")
			e := app.NewEditor(store)

			_, err := e.Add(t.Context(), tt.pattern, tt.why)

			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Add() error = %v, want %v", err, tt.wantErr)
			}
			saved, _ := store.Load(t.Context())
			if diff := cmp.Diff([]string{"github.com"}, entryPatterns(saved)); diff != "" {
				t.Fatalf("list changed on refusal (-want +got):\n%s", diff)
			}
		})
	}
}

func TestEditor_RemoveDeletesTheEntry(t *testing.T) {
	t.Parallel()
	store := nettest.NewStore()
	saveList(t, store, "github.com", "example.org")
	e := app.NewEditor(store)

	if err := e.Remove(t.Context(), "github.com"); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}

	saved, _ := store.Load(t.Context())
	if diff := cmp.Diff([]string{"example.org"}, entryPatterns(saved)); diff != "" {
		t.Fatalf("saved list mismatch (-want +got):\n%s", diff)
	}
}

func TestEditor_RemoveRefusesUnknownEntry(t *testing.T) {
	t.Parallel()
	store := nettest.NewStore()
	saveList(t, store, "github.com")
	e := app.NewEditor(store)

	if err := e.Remove(t.Context(), "example.org"); !errors.Is(err, domain.ErrEntryNotFound) {
		t.Fatalf("Remove() error = %v, want %v", err, domain.ErrEntryNotFound)
	}
}

func TestEditor_RemoveRefusesBadPattern(t *testing.T) {
	t.Parallel()
	e := app.NewEditor(nettest.NewStore())

	if err := e.Remove(t.Context(), "not a name"); !errors.Is(err, domain.ErrInvalidPattern) {
		t.Fatalf("Remove() error = %v, want %v", err, domain.ErrInvalidPattern)
	}
}
