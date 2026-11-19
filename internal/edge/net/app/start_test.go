// SPDX-License-Identifier: MIT

package app_test

import (
	"errors"
	"io"
	"log/slog"
	"net/netip"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/SDK-E/agentsone/internal/edge/net/app"
	"github.com/SDK-E/agentsone/internal/edge/net/domain"
	"github.com/SDK-E/agentsone/internal/edge/net/nettest"
)

type startFixture struct {
	store   *nettest.Store
	watcher *nettest.Watcher
	freeze  *nettest.FreezeStore
	deps    app.Deps
}

func newStartFixture() startFixture {
	f := startFixture{store: nettest.NewStore(), watcher: nettest.NewWatcher(), freeze: nettest.NewFreezeStore()}
	f.deps = app.Deps{
		Store:       f.store,
		Watcher:     f.watcher,
		FreezeStore: f.freeze,
		Clock:       nettest.NewClock(t0),
		Resolver:    nettest.NewResolver(),
		Observer:    nettest.NewObserver(),
		Log:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		Filter:      app.FilterConfig{Edge: edge},
	}
	return f
}

func start(t *testing.T, f startFixture) *app.Running {
	t.Helper()
	r, err := app.Start(t.Context(), f.deps)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(r.Stop)
	return r
}

func TestStart_WritesTheDefaultListOnFirstStart(t *testing.T) {
	t.Parallel()
	f := newStartFixture()

	start(t, f)

	saved, err := f.store.Load(t.Context())
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if diff := cmp.Diff(entryPatterns(domain.DefaultAllowList()), entryPatterns(saved)); diff != "" {
		t.Fatalf("saved list mismatch (-want +got):\n%s", diff)
	}
}

func TestStart_FiltersWithTheStoredList(t *testing.T) {
	t.Parallel()
	f := newStartFixture()
	saveList(t, f.store, "example.org")

	r := start(t, f)

	if diff := cmp.Diff([]string{"example.org"}, entryPatterns(r.Filter().AllowList())); diff != "" {
		t.Fatalf("filter list mismatch (-want +got):\n%s", diff)
	}
}

func TestStart_RefusesToStartWithAnUnreadableList(t *testing.T) {
	t.Parallel()
	f := newStartFixture()
	errBad := errors.New("line 3: bad pattern")
	f.store.FailLoadWith(errBad)

	_, err := app.Start(t.Context(), f.deps)

	if !errors.Is(err, errBad) {
		t.Fatalf("Start() error = %v, want %v", err, errBad)
	}
}

func TestStart_ComesBackFrozenIfItWasFrozen(t *testing.T) {
	t.Parallel()
	f := newStartFixture()
	if err := f.freeze.Save(t.Context(), true); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	r := start(t, f)

	if frozen, _ := r.Filter().Frozen(t.Context()); !frozen {
		t.Fatal("filter is not frozen after a restart")
	}
}

func TestStart_StartsFrozenWhenTheFreezeSwitchCannotBeRead(t *testing.T) {
	t.Parallel()
	f := newStartFixture()
	f.freeze.FailLoadWith(errors.New("unreadable"))

	r := start(t, f)

	if frozen, _ := r.Filter().Frozen(t.Context()); !frozen {
		t.Fatal("filter is not frozen when the switch cannot be read")
	}
}

func TestStart_RefusesAnInvalidEdgeAddress(t *testing.T) {
	t.Parallel()
	f := newStartFixture()
	f.deps.Filter.Edge = netipZero()

	if _, err := app.Start(t.Context(), f.deps); err == nil {
		t.Fatal("Start() accepted no edge address")
	}
}

func TestRunning_ReloadsTheListWhenTheStoreChanges(t *testing.T) {
	t.Parallel()
	f := newStartFixture()
	saveList(t, f.store, "example.org")
	r := start(t, f)

	saveList(t, f.store, "github.com")
	if err := f.watcher.Trigger(t.Context()); err != nil {
		t.Fatalf("Trigger() error = %v", err)
	}

	if diff := cmp.Diff([]string{"github.com"}, entryPatterns(r.Filter().AllowList())); diff != "" {
		t.Fatalf("filter list mismatch (-want +got):\n%s", diff)
	}
}

func TestRunning_KeepsTheLastGoodListWhenReloadFails(t *testing.T) {
	t.Parallel()
	f := newStartFixture()
	saveList(t, f.store, "example.org")
	r := start(t, f)

	f.store.FailLoadWith(errors.New("half written"))
	if err := f.watcher.Trigger(t.Context()); err != nil {
		t.Fatalf("Trigger() error = %v", err)
	}

	if diff := cmp.Diff([]string{"example.org"}, entryPatterns(r.Filter().AllowList())); diff != "" {
		t.Fatalf("filter list mismatch (-want +got):\n%s", diff)
	}
}

func TestRunning_StopTwiceIsHarmless(t *testing.T) {
	t.Parallel()
	r, err := app.Start(t.Context(), newStartFixture().deps)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	r.Stop()
	r.Stop()
}

func netipZero() netip.Addr { return netip.Addr{} }

func saveList(t *testing.T, s *nettest.Store, patterns ...string) {
	t.Helper()
	l := list(t, patterns...)
	if _, err := s.Update(t.Context(), func(domain.AllowList) (domain.AllowList, error) { return l, nil }); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
}

func entryPatterns(l domain.AllowList) []string {
	out := []string{}
	for _, e := range l.Entries() {
		out = append(out, e.Pattern.String())
	}
	return out
}
