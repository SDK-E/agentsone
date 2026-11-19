// SPDX-License-Identifier: MIT

package app_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	budgetapp "github.com/SDK-E/agentsone/internal/edge/budget/app"
	budget "github.com/SDK-E/agentsone/internal/edge/budget/domain"
)

const usd = budget.MicrosPerUSD

// fakeStore keeps state in memory and can be told to fail.
type fakeStore struct {
	mu        sync.Mutex
	settings  budgetapp.Settings
	spend     budgetapp.Spend
	saves     int
	failSave  error
	failLoad  error
	loadCalls int
}

func (f *fakeStore) LoadSettings(context.Context) (budgetapp.Settings, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.loadCalls++
	return f.settings, f.failLoad
}

func (f *fakeStore) SaveSettings(_ context.Context, s budgetapp.Settings) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failSave != nil {
		return f.failSave
	}
	f.settings = s
	return nil
}

func (f *fakeStore) LoadSpend(context.Context) (budgetapp.Spend, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.spend, f.failLoad
}

func (f *fakeStore) SaveSpend(_ context.Context, s budgetapp.Spend) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failSave != nil {
		return f.failSave
	}
	f.spend = s
	f.saves++
	return nil
}

func (f *fakeStore) setTotalOutside(m budget.Micros) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.settings.Total = m
}

func open(t *testing.T, st *fakeStore) *budgetapp.Service {
	t.Helper()
	svc, err := budgetapp.Open(context.Background(), st)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return svc
}

func TestOpenRestoresSettingsAndSpend(t *testing.T) {
	st := &fakeStore{
		settings: budgetapp.Settings{Total: 10 * usd, DefenceOn: true, DefenceSharePct: 20},
		spend:    budgetapp.Spend{General: 3 * usd, Defence: 1 * usd, Agents: map[string]budget.Micros{"worker": 3 * usd}},
	}
	got := open(t, st).State()
	if got.Total != 10*usd || got.Spent != 4*usd || !got.DefenceOn || got.Remaining != 5*usd {
		t.Fatalf("state = %+v", got)
	}
}

func TestOpenFailsWhenStoreFails(t *testing.T) {
	st := &fakeStore{failLoad: errors.New("disk gone")}
	if _, err := budgetapp.Open(context.Background(), st); err == nil {
		t.Fatal("Open succeeded with a failing store")
	}
}

func TestHumanSettingsAreSavedAtOnce(t *testing.T) {
	st := &fakeStore{}
	svc := open(t, st)
	ctx := context.Background()
	if err := svc.SetTotal(ctx, 25*usd); err != nil {
		t.Fatalf("SetTotal: %v", err)
	}
	if err := svc.SetDefenceShare(ctx, 30); err != nil {
		t.Fatalf("SetDefenceShare: %v", err)
	}
	if err := svc.SetDefence(ctx, true); err != nil {
		t.Fatalf("SetDefence: %v", err)
	}
	want := budgetapp.Settings{Total: 25 * usd, DefenceOn: true, DefenceSharePct: 30}
	if st.settings != want {
		t.Fatalf("saved settings = %+v, want %+v", st.settings, want)
	}
	if err := svc.SetDefenceShare(ctx, 101); !errors.Is(err, budget.ErrInvalidShare) {
		t.Fatalf("SetDefenceShare(101) error = %v", err)
	}
	if err := svc.SetTotal(ctx, -1); !errors.Is(err, budget.ErrInvalidAmount) {
		t.Fatalf("SetTotal(-1) error = %v", err)
	}
}

func TestSettingNotSavedLeavesLedgerUnchanged(t *testing.T) {
	st := &fakeStore{settings: budgetapp.Settings{Total: 5 * usd}}
	svc := open(t, st)
	st.failSave = errors.New("read-only")
	if err := svc.SetTotal(context.Background(), 50*usd); err == nil {
		t.Fatal("SetTotal succeeded although the store failed")
	}
	if got := svc.State().Total; got != 5*usd {
		t.Fatalf("total = %v, want unchanged 5 USD", got)
	}
}

func TestReserveSettleAndFlush(t *testing.T) {
	st := &fakeStore{settings: budgetapp.Settings{Total: 10 * usd}}
	svc := open(t, st)
	ctx := context.Background()

	id, err := svc.Reserve(budget.Caller{Agent: "worker"}, 2*usd)
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if err := svc.Settle(id, 1*usd); err != nil {
		t.Fatalf("Settle: %v", err)
	}
	if err := svc.Flush(ctx); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if st.spend.General != 1*usd || st.spend.Agents["worker"] != 1*usd {
		t.Fatalf("saved spend = %+v", st.spend)
	}
	// Nothing changed: no second write.
	if err := svc.Flush(ctx); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if st.saves != 1 {
		t.Fatalf("saves = %d, want 1", st.saves)
	}
	if err := svc.Release(999); !errors.Is(err, budget.ErrUnknownReservation) {
		t.Fatalf("Release(unknown) error = %v", err)
	}
}

func TestFlushPicksUpSettingsChangedByAnotherProcess(t *testing.T) {
	st := &fakeStore{settings: budgetapp.Settings{Total: 1 * usd}}
	svc := open(t, st)
	st.setTotalOutside(7 * usd)
	if err := svc.Flush(context.Background()); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if got := svc.State().Total; got != 7*usd {
		t.Fatalf("total = %v, want 7 USD", got)
	}
}

func TestFlushReportsStoreErrors(t *testing.T) {
	st := &fakeStore{settings: budgetapp.Settings{Total: 10 * usd}}
	svc := open(t, st)
	id, _ := svc.Reserve(budget.Caller{Agent: "w"}, 1)
	_ = svc.Settle(id, 1)
	st.failSave = errors.New("full")
	if err := svc.Flush(context.Background()); err == nil {
		t.Fatal("Flush succeeded although the store failed")
	}
	st.failSave = nil
	st.failLoad = errors.New("locked")
	if err := svc.Flush(context.Background()); err == nil {
		t.Fatal("Flush succeeded although settings could not load")
	}
}

func TestRunFlushesOnEveryTickAndOnStop(t *testing.T) {
	st := &fakeStore{settings: budgetapp.Settings{Total: 10 * usd}}
	svc := open(t, st)
	ticks := make(chan time.Time)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- svc.Run(ctx, ticks, nil) }()

	id, _ := svc.Reserve(budget.Caller{Agent: "w"}, 2*usd)
	_ = svc.Settle(id, 2*usd)
	ticks <- time.Time{}
	ticks <- time.Time{} // second tick waits for the first flush to finish

	st.mu.Lock()
	first := st.spend.General
	st.mu.Unlock()
	if first != 2*usd {
		t.Fatalf("after tick spend = %v, want 2 USD", first)
	}

	id, _ = svc.Reserve(budget.Caller{Agent: "w"}, 1*usd)
	_ = svc.Settle(id, 1*usd)
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run: %v", err)
	}
	if st.spend.General != 3*usd {
		t.Fatalf("after stop spend = %v, want 3 USD", st.spend.General)
	}
}

func TestRunReportsFlushErrorsAndKeepsGoing(t *testing.T) {
	st := &fakeStore{settings: budgetapp.Settings{Total: 10 * usd}}
	svc := open(t, st)
	st.failLoad = errors.New("locked")
	ticks := make(chan time.Time)
	errs := make(chan error, 4)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- svc.Run(ctx, ticks, func(err error) { errs <- err }) }()
	ticks <- time.Time{}
	if err := <-errs; err == nil {
		t.Fatal("no error reported")
	}
	st.mu.Lock()
	st.failLoad = nil
	st.mu.Unlock()
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run: %v", err)
	}
}

func TestAllocationsAndRemaining(t *testing.T) {
	st := &fakeStore{settings: budgetapp.Settings{Total: 10 * usd}}
	svc := open(t, st)
	svc.SetAllocations(map[string]budget.Micros{"worker": 1 * usd})
	if got := svc.RemainingFor(budget.Caller{Agent: "worker"}); got != 1*usd {
		t.Fatalf("remaining = %v, want 1 USD", got)
	}
	if _, err := svc.Reserve(budget.Caller{Agent: "worker"}, 2*usd); !errors.Is(err, budget.ErrAllocationExhausted) {
		t.Fatalf("Reserve error = %v", err)
	}
}

func TestConcurrentUseIsSafe(t *testing.T) {
	st := &fakeStore{settings: budgetapp.Settings{Total: 1000 * usd}}
	svc := open(t, st)
	var wg sync.WaitGroup
	for i := range 50 {
		wg.Go(func() {
			id, err := svc.Reserve(budget.Caller{Agent: "w"}, usd)
			if err != nil {
				t.Errorf("Reserve %d: %v", i, err)
				return
			}
			_ = svc.Settle(id, usd)
			_ = svc.State()
		})
	}
	wg.Wait()
	if got := svc.State().Spent; got != 50*usd {
		t.Fatalf("spent = %v, want 50 USD", got)
	}
}
