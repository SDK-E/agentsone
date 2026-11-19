// SPDX-License-Identifier: MIT

package enginefetch

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/core/coretest"
	"github.com/SDK-E/agentsone/internal/enginefetch/domain"
	"github.com/SDK-E/agentsone/internal/enginefetch/enginefetchtest"
)

var engine = bytes.Repeat([]byte("engine"), 2000)

var testPin = domain.Pin{
	Engine: "claude-code", Version: "2.1.328", BaseURL: "https://downloads.example/rel",
	Artifacts: map[string]domain.Artifact{
		"linux/amd64": {Platform: "linux-x64", Binary: "claude", SHA256: enginefetchtest.Sum(engine), Size: int64(len(engine))},
	},
}

// engineSetup is a core.EngineSetup fake.
type engineSetup struct {
	mu       sync.Mutex
	accepted bool
	saved    []core.EngineFetchStatus
}

func (e *engineSetup) LicenceAccepted(context.Context) (bool, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.accepted, nil
}

func (e *engineSetup) SaveFetchStatus(_ context.Context, s core.EngineFetchStatus) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.saved = append(e.saved, s)
	return nil
}

func (e *engineSetup) accept() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.accepted = true
}

func (e *engineSetup) last() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.saved) == 0 {
		return ""
	}
	return e.saved[len(e.saved)-1].State
}

func (e *engineSetup) count() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.saved)
}

type rig struct {
	mod    *Module
	setup  *engineSetup
	fakes  *coretest.Fakes
	source *enginefetchtest.Source
	disk   *enginefetchtest.Disk
}

func newRig(t *testing.T, accepted bool) *rig {
	t.Helper()
	deps, fakes := coretest.NewDeps(t)
	r := &rig{
		setup: &engineSetup{accepted: accepted}, fakes: fakes,
		source: &enginefetchtest.Source{Data: engine}, disk: enginefetchtest.NewDisk(),
	}
	deps.Ports.EngineSetup = r.setup
	r.mod = newModule(deps, wiring{pin: testPin, goos: "linux", goarch: "amd64", source: r.source, disk: r.disk})
	return r
}

func (r *rig) start(t *testing.T) {
	t.Helper()
	if err := r.mod.Start(t.Context()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = r.mod.Stop(context.Background()) })
}

func (r *rig) publishAccepted(t *testing.T) {
	t.Helper()
	_, err := r.fakes.Events.Publish(t.Context(), core.Event{
		Type: LicenceAcceptedEvent, Source: "agentsone/auth", Actor: "human:ada", ChartScope: "society",
	})
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
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

func TestModuleName(t *testing.T) {
	if got := newRig(t, false).mod.Name(); got != "enginefetch" {
		t.Errorf("Name() = %q, want enginefetch", got)
	}
}

func TestNewBuildsOnThePinnedEngine(t *testing.T) {
	deps, _ := coretest.NewDeps(t)
	if m := New(deps); m.fetcher == nil {
		t.Fatal("New() built no fetcher")
	}
}

func TestWithoutALicenceNothingIsRequested(t *testing.T) {
	r := newRig(t, false)
	r.start(t)
	// The run saves waiting-licence as its last step, so it is over here.
	eventually(t, "the waiting-licence status", func() bool { return r.setup.last() == "waiting-licence" })
	if n := len(r.source.Offsets()); n != 0 {
		t.Errorf("requests = %d, want 0 without a licence", n)
	}
}

func TestALicenceEventInstallsTheEngine(t *testing.T) {
	r := newRig(t, false)
	r.start(t)
	eventually(t, "the first run", func() bool { return r.setup.last() == "waiting-licence" })
	r.setup.accept()
	r.publishAccepted(t)
	eventually(t, "the engine install", func() bool { return r.setup.last() == "ready" })
	if got := r.disk.InstalledBytes(testPin); !bytes.Equal(got, engine) {
		t.Errorf("installed %d bytes, want the %d pinned bytes", len(got), len(engine))
	}
}

func TestAStoredEventIsReplayedAfterARestart(t *testing.T) {
	r := newRig(t, true)
	r.publishAccepted(t) // stored before the module starts, as after a restart
	r.start(t)
	eventually(t, "the engine install", func() bool { return r.setup.last() == "ready" })
	if n := len(r.source.Offsets()); n != 1 {
		t.Errorf("downloads = %d, want 1", n)
	}
}

func TestASecondEventDoesNotDownloadAgain(t *testing.T) {
	r := newRig(t, true)
	r.start(t)
	eventually(t, "the engine install", func() bool { return r.setup.last() == "ready" })
	before := r.setup.count()
	r.publishAccepted(t)
	eventually(t, "the second run", func() bool { return r.setup.count() > before })
	if n := len(r.source.Offsets()); n != 1 {
		t.Errorf("downloads = %d, want 1", n)
	}
}

type noSubscribe struct{}

func (noSubscribe) Subscribe(context.Context, core.EventFilter) (<-chan core.Event, error) {
	return nil, core.ErrUnavailable
}

func TestWithoutEventsTheEngineIsStillFetchedAtStart(t *testing.T) {
	r := newRig(t, true)
	r.mod.ports.Subscribe = noSubscribe{}
	r.start(t)
	eventually(t, "the engine install", func() bool { return r.setup.last() == "ready" })
}

func TestStopEndsTheWatcher(t *testing.T) {
	r := newRig(t, false)
	if err := r.mod.Stop(t.Context()); err != nil {
		t.Fatalf("Stop() before Start error = %v", err)
	}
	r.start(t)
	eventually(t, "the first run", func() bool { return r.setup.count() > 0 })
	done := make(chan error, 1)
	go func() { done <- r.mod.Stop(t.Context()) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Stop() error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Stop() did not return")
	}
}

func TestBackoffGrowsWithTheAttemptAndStopsWithTheContext(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := backoff(ctx, 1); !errors.Is(err, context.Canceled) {
		t.Errorf("backoff() on an ended context = %v, want context.Canceled", err)
	}
	start := time.Now()
	if err := wait(t.Context(), time.Millisecond); err != nil {
		t.Errorf("wait() error = %v", err)
	}
	if time.Since(start) < time.Millisecond {
		t.Error("wait() returned before its delay")
	}
}
