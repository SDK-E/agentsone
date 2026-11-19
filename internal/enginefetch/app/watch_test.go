// SPDX-License-Identifier: MIT

package app_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/enginefetch/domain"
)

// watch runs Watch in the background and returns a stop function that waits
// for it to return.
func watch(t *testing.T, r *rig, triggers <-chan core.Event) (errs func() []error, stop func()) {
	t.Helper()
	f := r.fetcher(t)
	ctx, cancel := context.WithCancel(t.Context())
	var mu sync.Mutex
	var got []error
	done := make(chan struct{})
	go func() {
		defer close(done)
		f.Watch(ctx, triggers, func(err error) {
			mu.Lock()
			defer mu.Unlock()
			got = append(got, err)
		})
	}()
	errs = func() []error {
		mu.Lock()
		defer mu.Unlock()
		return append([]error(nil), got...)
	}
	stop = func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("Watch did not return after its context ended")
		}
	}
	t.Cleanup(stop)
	return errs, stop
}

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for !ok() {
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for %s", what)
		case <-tick.C:
		}
	}
}

func TestWatchWithoutLicenceDownloadsNothingAndReportsNoError(t *testing.T) {
	r := newRig(false)
	errs, stop := watch(t, r, make(chan core.Event))
	eventually(t, "the waiting-licence status", func() bool { return r.sink.Last().State == domain.StateWaitingLicence })
	stop()
	if n := len(r.source.Offsets()); n != 0 {
		t.Errorf("requests without a licence = %d, want 0", n)
	}
	if got := errs(); len(got) != 0 {
		t.Errorf("errors reported = %v, want none (a missing licence is not an error)", got)
	}
}

func TestWatchInstallsAfterATrigger(t *testing.T) {
	r := newRig(false)
	triggers := make(chan core.Event)
	_, stop := watch(t, r, triggers)
	eventually(t, "the first run", func() bool { return r.sink.Last().State == domain.StateWaitingLicence })
	r.gate.Accept()
	triggers <- core.Event{Type: "agentsone.setup.licence.accepted"}
	eventually(t, "the engine install", func() bool { return r.sink.Last().State == domain.StateReady })
	stop()
	if got := r.disk.InstalledBytes(r.pin); len(got) != len(engine) {
		t.Errorf("installed %d bytes, want %d", len(got), len(engine))
	}
}

func TestWatchInstallsAtStartWhenTheLicenceIsAlreadyAccepted(t *testing.T) {
	r := newRig(true)
	_, stop := watch(t, r, nil)
	eventually(t, "the engine install", func() bool { return r.sink.Last().State == domain.StateReady })
	stop()
}

func TestWatchDoesNotDownloadAgainOnALaterTrigger(t *testing.T) {
	r := newRig(true)
	triggers := make(chan core.Event)
	_, stop := watch(t, r, triggers)
	eventually(t, "the engine install", func() bool { return r.sink.Last().State == domain.StateReady })
	runs := len(r.sink.All())
	triggers <- core.Event{}
	eventually(t, "the second run", func() bool { return len(r.sink.All()) > runs })
	stop()
	if n := len(r.source.Offsets()); n != 1 {
		t.Errorf("downloads = %d, want 1 (the second trigger finds the engine installed)", n)
	}
}

func TestWatchFoldsABurstOfTriggersIntoOneRun(t *testing.T) {
	r := newRig(false)
	block := make(chan struct{})
	r.gate.Block(block) // the first run holds in the licence check
	triggers := make(chan core.Event, 10)
	_, stop := watch(t, r, triggers)
	eventually(t, "the first run to start", func() bool { return r.gate.Calls() == 1 })
	for range 10 {
		triggers <- core.Event{}
	}
	eventually(t, "the burst to be read", func() bool { return len(triggers) == 0 })
	close(block)
	eventually(t, "the folded run to finish", func() bool { return len(r.sink.All()) == 2 })
	stop()
	if n := r.gate.Calls(); n != 2 {
		t.Errorf("licence checks = %d, want 2 (one at start, one for the whole burst)", n)
	}
}

func TestWatchReportsFailuresButNotAMissingLicence(t *testing.T) {
	r := newRig(true)
	boom := errors.New("network down")
	r.source.FailNext = []error{boom, boom, boom}
	errs, stop := watch(t, r, nil)
	eventually(t, "the failure report", func() bool { return len(errs()) == 1 })
	stop()
	if got := errs(); !errors.Is(got[0], boom) {
		t.Errorf("reported %v, want %v", got[0], boom)
	}
}

func TestWatchStopsWhenTriggersClose(t *testing.T) {
	r := newRig(false)
	triggers := make(chan core.Event)
	_, stop := watch(t, r, triggers)
	eventually(t, "the first run", func() bool { return r.gate.Calls() == 1 })
	close(triggers)
	stop() // Watch still returns on its context; a closed trigger channel is not a busy loop
	if n := r.gate.Calls(); n != 1 {
		t.Errorf("licence checks = %d, want 1", n)
	}
}
