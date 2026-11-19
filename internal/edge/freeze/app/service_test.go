// SPDX-License-Identifier: MIT

package app_test

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/edge/freeze/app"
	"github.com/SDK-E/agentsone/internal/edge/freeze/domain"
)

var t0 = time.Date(2026, 11, 19, 21, 0, 0, 0, time.UTC)

type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time { return c.now }

type fakeEgress struct {
	mu         sync.Mutex
	frozen     bool
	freezeErr  error
	releaseErr error
	frozenErr  error
}

func (e *fakeEgress) Freeze(context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.frozen = true // the filter stays frozen even when saving fails
	return e.freezeErr
}

func (e *fakeEgress) Release(context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.releaseErr != nil {
		return e.releaseErr
	}
	e.frozen = false
	return nil
}

func (e *fakeEgress) Frozen(context.Context) (bool, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.frozen, e.frozenErr
}

type fakeStore struct {
	mu      sync.Mutex
	state   domain.State
	loadErr error
	saveErr error
	saves   int
}

func (s *fakeStore) Load(context.Context) (domain.State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state, s.loadErr
}

func (s *fakeStore) Save(_ context.Context, st domain.State) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.saveErr != nil {
		return s.saveErr
	}
	s.state = st
	s.saves++
	return nil
}

type fakeProofs struct {
	mu   sync.Mutex
	got  []app.Proof
	fail error
}

func (p *fakeProofs) RecordProof(_ context.Context, pr app.Proof) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.got = append(p.got, pr)
	return p.fail
}

func (p *fakeProofs) actions() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]string, 0, len(p.got))
	for _, pr := range p.got {
		out = append(out, pr.Action+" "+pr.Outcome)
	}
	return out
}

type rig struct {
	egress *fakeEgress
	store  *fakeStore
	proofs *fakeProofs
	clock  *fakeClock
}

func newRig() *rig {
	return &rig{egress: &fakeEgress{}, store: &fakeStore{}, proofs: &fakeProofs{}, clock: &fakeClock{now: t0}}
}

func (r *rig) deps() app.Deps {
	return app.Deps{Egress: r.egress, Store: r.store, Proofs: r.proofs, Clock: r.clock, Log: slog.New(slog.DiscardHandler)}
}

func (r *rig) open(t *testing.T) *app.Service {
	t.Helper()
	s, err := app.Open(t.Context(), r.deps())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return s
}

func frozen(t *testing.T, s *app.Service) bool {
	t.Helper()
	on, err := s.Frozen(t.Context())
	if err != nil {
		t.Fatalf("Frozen: %v", err)
	}
	return on
}

func equal(a, b []string) bool { return slices.Equal(a, b) }

func TestOpen_RefusesMissingPorts(t *testing.T) {
	t.Parallel()
	r := newRig()
	for name, d := range map[string]app.Deps{
		"egress": {Store: r.store, Clock: r.clock},
		"store":  {Egress: r.egress, Clock: r.clock},
		"clock":  {Egress: r.egress, Store: r.store},
	} {
		if _, err := app.Open(t.Context(), d); !errors.Is(err, app.ErrMissingPort) {
			t.Errorf("Open without %s = %v, want ErrMissingPort", name, err)
		}
	}
}

func TestOpen_StartsOpenWhenNothingWasSaved(t *testing.T) {
	t.Parallel()
	r := newRig()
	s := r.open(t)
	if frozen(t, s) || r.egress.frozen {
		t.Fatal("fresh switch is frozen")
	}
}

func TestOpen_StaysFrozenAfterRestart(t *testing.T) {
	t.Parallel()
	r := newRig()
	r.store.state = domain.State{Frozen: true, Since: t0}
	s := r.open(t)
	if !frozen(t, s) || !r.egress.frozen {
		t.Fatalf("after restart frozen=%v egress=%v, want both frozen", frozen(t, s), r.egress.frozen)
	}
	if s.Status().Since != t0 {
		t.Fatalf("Since = %v, want the saved %v", s.Status().Since, t0)
	}
}

func TestOpen_FailsWhenSavedFreezeCannotCutEgress(t *testing.T) {
	t.Parallel()
	r := newRig()
	r.store.state = domain.State{Frozen: true, Since: t0}
	r.egress.freezeErr = errors.New("disk full")
	if _, err := app.Open(t.Context(), r.deps()); err == nil {
		t.Fatal("Open succeeded although egress could not be cut")
	}
}

func TestOpen_AdoptsAFreezeTheFilterAlreadyHolds(t *testing.T) {
	t.Parallel()
	r := newRig()
	r.egress.frozen = true
	s := r.open(t)
	if !frozen(t, s) || !r.store.state.Frozen {
		t.Fatal("switch did not adopt the filter's freeze")
	}
	if got, want := r.proofs.actions(), []string{"freeze.adopt success"}; !equal(got, want) {
		t.Fatalf("proofs = %v, want %v", got, want)
	}
}

func TestOpen_FailsClosedOnUnreadableState(t *testing.T) {
	t.Parallel()
	r := newRig()
	r.store.loadErr = errors.New("bad json")
	s := r.open(t)
	if !frozen(t, s) || !r.egress.frozen {
		t.Fatal("unreadable state did not freeze")
	}
}

func TestOpen_FailsClosedWhenTheFilterCannotSay(t *testing.T) {
	t.Parallel()
	r := newRig()
	r.egress.frozenErr = errors.New("filter down")
	s := r.open(t)
	if !frozen(t, s) {
		t.Fatal("unknown filter state did not freeze")
	}
}

func TestFreeze_CutsEgressSavesAndRecordsProof(t *testing.T) {
	t.Parallel()
	r := newRig()
	s := r.open(t)
	r.clock.now = t0.Add(time.Hour)
	if err := s.Freeze(t.Context()); err != nil {
		t.Fatalf("Freeze: %v", err)
	}
	if !frozen(t, s) || !r.egress.frozen || !r.store.state.Frozen {
		t.Fatalf("frozen=%v egress=%v saved=%v, want all true", frozen(t, s), r.egress.frozen, r.store.state.Frozen)
	}
	if got := s.Status().Since; !got.Equal(t0.Add(time.Hour)) {
		t.Fatalf("Since = %v, want freeze time", got)
	}
	if got, want := r.proofs.actions(), []string{"freeze.on success"}; !equal(got, want) {
		t.Fatalf("proofs = %v, want %v", got, want)
	}
}

func TestFreeze_TwiceRecordsOneChange(t *testing.T) {
	t.Parallel()
	r := newRig()
	s := r.open(t)
	for range 2 {
		if err := s.Freeze(t.Context()); err != nil {
			t.Fatalf("Freeze: %v", err)
		}
	}
	if got, want := r.proofs.actions(), []string{"freeze.on success"}; !equal(got, want) {
		t.Fatalf("proofs = %v, want %v", got, want)
	}
	if r.store.saves != 1 {
		t.Fatalf("saves = %d, want 1", r.store.saves)
	}
}

func TestFreeze_StaysFrozenWhenSavingFails(t *testing.T) {
	t.Parallel()
	r := newRig()
	s := r.open(t)
	r.store.saveErr = errors.New("disk full")
	if err := s.Freeze(t.Context()); err == nil {
		t.Fatal("Freeze hid the save error")
	}
	if !frozen(t, s) || !r.egress.frozen {
		t.Fatal("a failed save opened the switch")
	}
	if got, want := r.proofs.actions(), []string{"freeze.on failure"}; !equal(got, want) {
		t.Fatalf("proofs = %v, want %v", got, want)
	}
}

func TestFreeze_GatesModelsEvenWhenEgressFails(t *testing.T) {
	t.Parallel()
	r := newRig()
	s := r.open(t)
	r.egress.freezeErr = errors.New("filter down")
	if err := s.Freeze(t.Context()); err == nil {
		t.Fatal("Freeze hid the egress error")
	}
	if !frozen(t, s) {
		t.Fatal("model gate is open after a failed egress cut")
	}
}

func TestFreeze_KeepsWorkingWhenProofFails(t *testing.T) {
	t.Parallel()
	r := newRig()
	r.proofs.fail = errors.New("audit down")
	s := r.open(t)
	if err := s.Freeze(t.Context()); err != nil {
		t.Fatalf("Freeze failed because of the proof: %v", err)
	}
	if !frozen(t, s) {
		t.Fatal("not frozen")
	}
}

func TestRelease_RestoresEgressSavesAndRecordsProof(t *testing.T) {
	t.Parallel()
	r := newRig()
	r.store.state = domain.State{Frozen: true, Since: t0}
	s := r.open(t)
	if err := s.Release(t.Context()); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if frozen(t, s) || r.egress.frozen || r.store.state.Frozen {
		t.Fatal("something stayed frozen after release")
	}
	if got, want := r.proofs.actions(), []string{"freeze.off success"}; !equal(got, want) {
		t.Fatalf("proofs = %v, want %v", got, want)
	}
}

func TestRelease_WhenOpenDoesNothing(t *testing.T) {
	t.Parallel()
	r := newRig()
	s := r.open(t)
	if err := s.Release(t.Context()); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if len(r.proofs.actions()) != 0 || r.store.saves != 0 {
		t.Fatal("releasing an open switch recorded or saved something")
	}
}

func TestRelease_StaysFrozenWhenEgressCannotOpen(t *testing.T) {
	t.Parallel()
	r := newRig()
	r.store.state = domain.State{Frozen: true, Since: t0}
	s := r.open(t)
	r.egress.releaseErr = errors.New("save failed")
	if err := s.Release(t.Context()); err == nil {
		t.Fatal("Release hid the egress error")
	}
	if !frozen(t, s) || !r.store.state.Frozen {
		t.Fatal("model gate or saved state opened although egress stayed cut")
	}
	if got, want := r.proofs.actions(), []string{"freeze.off failure"}; !equal(got, want) {
		t.Fatalf("proofs = %v, want %v", got, want)
	}
}

func TestRelease_StaysFrozenWhenSavingFails(t *testing.T) {
	t.Parallel()
	r := newRig()
	r.store.state = domain.State{Frozen: true, Since: t0}
	s := r.open(t)
	r.store.saveErr = errors.New("disk full")
	if err := s.Release(t.Context()); err == nil {
		t.Fatal("Release hid the save error")
	}
	if !frozen(t, s) || !r.egress.frozen {
		t.Fatal("switch opened although the release was not saved")
	}
}

func TestService_WorksWithoutProofs(t *testing.T) {
	t.Parallel()
	r := newRig()
	d := r.deps()
	d.Proofs = nil
	d.Log = nil
	s, err := app.Open(t.Context(), d)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := s.Freeze(t.Context()); err != nil {
		t.Fatalf("Freeze: %v", err)
	}
	if err := s.Release(t.Context()); err != nil {
		t.Fatalf("Release: %v", err)
	}
}
