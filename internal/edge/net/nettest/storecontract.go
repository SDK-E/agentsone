// SPDX-License-Identifier: MIT

package nettest

import (
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"

	"github.com/SDK-E/agentsone/internal/edge/net/app"
	"github.com/SDK-E/agentsone/internal/edge/net/domain"
)

// RunAllowListStoreContract is the behaviour every app.AllowListStore must have. It runs against
// the file store and the in-memory fake, which keeps the fake honest.
func RunAllowListStoreContract(t *testing.T, newStore func(t *testing.T) app.AllowListStore) {
	t.Helper()

	t.Run("loads the default list when nothing is saved", func(t *testing.T) {
		t.Parallel()
		s := newStore(t)

		got, err := s.Load(t.Context())

		if err != nil || !slices.Equal(patternsOf(got), patternsOf(domain.DefaultAllowList())) {
			t.Fatalf("Load() = %v, %v; want the default list", patternsOf(got), err)
		}
	})

	t.Run("init saves the default list", func(t *testing.T) {
		t.Parallel()
		s := newStore(t)

		if err := s.Init(t.Context()); err != nil {
			t.Fatalf("Init() error = %v", err)
		}

		got, err := s.Load(t.Context())
		if err != nil || !slices.Equal(patternsOf(got), patternsOf(domain.DefaultAllowList())) {
			t.Fatalf("Load() = %v, %v; want the default list", patternsOf(got), err)
		}
	})

	t.Run("init keeps a saved list", func(t *testing.T) {
		t.Parallel()
		s := newStore(t)
		replace(t, s, "example.org")

		if err := s.Init(t.Context()); err != nil {
			t.Fatalf("Init() error = %v", err)
		}

		got, err := s.Load(t.Context())
		if err != nil || !slices.Equal(patternsOf(got), []string{"example.org"}) {
			t.Fatalf("Load() = %v, %v; want [example.org]", patternsOf(got), err)
		}
	})

	t.Run("update saves the changed list with reasons", func(t *testing.T) {
		t.Parallel()
		s := newStore(t)
		replace(t, s, "example.org", "*.example.net")

		got, err := s.Load(t.Context())
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}
		entries := got.Entries()
		if !slices.Equal(patternsOf(got), []string{"*.example.net", "example.org"}) || entries[0].Why != "why *.example.net" {
			t.Fatalf("Load() = %+v; want both entries with reasons", entries)
		}
	})

	t.Run("update starts from the default list when nothing is saved", func(t *testing.T) {
		t.Parallel()
		s := newStore(t)

		got, err := s.Update(t.Context(), func(l domain.AllowList) (domain.AllowList, error) { return l, nil })

		if err != nil || !slices.Equal(patternsOf(got), patternsOf(domain.DefaultAllowList())) {
			t.Fatalf("Update() = %v, %v; want the default list", patternsOf(got), err)
		}
	})

	t.Run("failed update saves nothing", func(t *testing.T) {
		t.Parallel()
		s := newStore(t)
		replace(t, s, "example.org")
		errChange := errors.New("refused")

		_, err := s.Update(t.Context(), func(domain.AllowList) (domain.AllowList, error) { return domain.AllowList{}, errChange })

		got, loadErr := s.Load(t.Context())
		if !errors.Is(err, errChange) || loadErr != nil || !slices.Equal(patternsOf(got), []string{"example.org"}) {
			t.Fatalf("Update() error = %v, list = %v; want %v and the list unchanged", err, patternsOf(got), errChange)
		}
	})

	t.Run("concurrent updates lose no change", func(t *testing.T) {
		t.Parallel()
		s := newStore(t)
		replace(t, s)
		const writers = 8
		var wg sync.WaitGroup
		errs := make(chan error, writers)
		for i := range writers {
			wg.Go(func() {
				p, _ := domain.ParsePattern(fmt.Sprintf("host%d.example.org", i))
				_, err := s.Update(t.Context(), func(l domain.AllowList) (domain.AllowList, error) {
					return l.With(domain.Entry{Pattern: p, Why: "concurrent"})
				})
				errs <- err
			})
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatalf("Update() error = %v", err)
			}
		}

		got, err := s.Load(t.Context())
		if err != nil || len(got.Entries()) != writers {
			t.Fatalf("Load() has %d entries, %v; want %d", len(got.Entries()), err, writers)
		}
	})
}

func replace(t *testing.T, s app.AllowListStore, patterns ...string) {
	t.Helper()
	entries := make([]domain.Entry, 0, len(patterns))
	for _, p := range patterns {
		pat, err := domain.ParsePattern(p)
		if err != nil {
			t.Fatalf("ParsePattern(%q) error = %v", p, err)
		}
		entries = append(entries, domain.Entry{Pattern: pat, Why: "why " + p})
	}
	l, err := domain.NewAllowList(entries)
	if err != nil {
		t.Fatalf("NewAllowList() error = %v", err)
	}
	if _, err := s.Update(t.Context(), func(domain.AllowList) (domain.AllowList, error) { return l, nil }); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
}

func patternsOf(l domain.AllowList) []string {
	out := []string{}
	for _, e := range l.Entries() {
		out = append(out, e.Pattern.String())
	}
	return out
}
