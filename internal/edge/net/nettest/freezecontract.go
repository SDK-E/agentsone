// SPDX-License-Identifier: MIT

package nettest

import (
	"testing"

	"github.com/SDK-E/agentsone/internal/edge/net/app"
)

// RunFreezeStoreContract is the behaviour every app.FreezeStore must have. It runs against the
// file store and the in-memory fake.
func RunFreezeStoreContract(t *testing.T, newStore func(t *testing.T) app.FreezeStore) {
	t.Helper()

	t.Run("starts not frozen", func(t *testing.T) {
		t.Parallel()
		got, err := newStore(t).Load(t.Context())
		if err != nil || got {
			t.Fatalf("Load() = %v, %v; want false", got, err)
		}
	})

	t.Run("keeps a freeze", func(t *testing.T) {
		t.Parallel()
		s := newStore(t)
		if err := s.Save(t.Context(), true); err != nil {
			t.Fatalf("Save(true) error = %v", err)
		}
		got, err := s.Load(t.Context())
		if err != nil || !got {
			t.Fatalf("Load() = %v, %v; want true", got, err)
		}
	})

	t.Run("keeps a release", func(t *testing.T) {
		t.Parallel()
		s := newStore(t)
		if err := s.Save(t.Context(), true); err != nil {
			t.Fatalf("Save(true) error = %v", err)
		}
		if err := s.Save(t.Context(), false); err != nil {
			t.Fatalf("Save(false) error = %v", err)
		}
		got, err := s.Load(t.Context())
		if err != nil || got {
			t.Fatalf("Load() = %v, %v; want false", got, err)
		}
	})

	t.Run("releasing twice is harmless", func(t *testing.T) {
		t.Parallel()
		s := newStore(t)
		if err := s.Save(t.Context(), false); err != nil {
			t.Fatalf("Save(false) error = %v", err)
		}
		if err := s.Save(t.Context(), false); err != nil {
			t.Fatalf("second Save(false) error = %v", err)
		}
	})
}
