// SPDX-License-Identifier: MIT

package app_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/SDK-E/agentsone/internal/core/coretest"
	"github.com/SDK-E/agentsone/internal/enginefetch/app"
	"github.com/SDK-E/agentsone/internal/enginefetch/domain"
	"github.com/SDK-E/agentsone/internal/enginefetch/enginefetchtest"
)

var engine = bytes.Repeat([]byte("claude-engine-"), 1000) // 14000 bytes

type rig struct {
	pin    domain.Pin
	gate   *enginefetchtest.Gate
	sink   *enginefetchtest.Sink
	source *enginefetchtest.Source
	disk   *enginefetchtest.Disk
	clock  *coretest.Clock
	goarch string
}

func newRig(accepted bool) *rig {
	return &rig{
		pin: domain.Pin{
			Engine: "claude-code", Version: "2.1.328", BaseURL: "https://downloads.example/rel",
			Artifacts: map[string]domain.Artifact{
				"linux/amd64": {Platform: "linux-x64", Binary: "claude", SHA256: enginefetchtest.Sum(engine), Size: int64(len(engine))},
			},
		},
		gate:   enginefetchtest.NewGate(accepted),
		sink:   &enginefetchtest.Sink{},
		source: &enginefetchtest.Source{Data: engine},
		disk:   enginefetchtest.NewDisk(),
		clock:  coretest.NewClock(time.Date(2026, 11, 19, 8, 0, 0, 0, time.UTC)),
		goarch: "amd64",
	}
}

func (r *rig) fetcher(t *testing.T) *app.Fetcher {
	t.Helper()
	f, err := app.New(app.Deps{
		Pin: r.pin, GOOS: "linux", GOARCH: r.goarch,
		Licence: r.gate, Status: r.sink, Source: r.source, Disk: r.disk, Clock: r.clock,
		ReportEvery: 4096,
	})
	if err != nil {
		t.Fatalf("app.New() error = %v", err)
	}
	return f
}

func TestEnsure_DoesNotDownloadWithoutAnAcceptedLicence(t *testing.T) {
	t.Parallel()
	r := newRig(false)

	st, err := r.fetcher(t).Ensure(t.Context())

	if !errors.Is(err, app.ErrLicenceNotAccepted) {
		t.Fatalf("Ensure() error = %v, want %v", err, app.ErrLicenceNotAccepted)
	}
	if st.State != domain.StateWaitingLicence || r.sink.Last().State != domain.StateWaitingLicence {
		t.Fatalf("status = %q, saved %q, want waiting-licence", st.State, r.sink.Last().State)
	}
	if got := r.source.URLs(); len(got) != 0 {
		t.Fatalf("download requests = %v, want none", got)
	}
	if r.disk.HasPart(r.pin) || r.disk.InstalledBytes(r.pin) != nil {
		t.Fatal("something was written to disk without a licence")
	}
}

func TestEnsure_DownloadsVerifiesAndInstallsThePinnedEngine(t *testing.T) {
	t.Parallel()
	r := newRig(true)

	st, err := r.fetcher(t).Ensure(t.Context())
	if err != nil {
		t.Fatalf("Ensure() error = %v", err)
	}

	if !bytes.Equal(r.disk.InstalledBytes(r.pin), engine) {
		t.Fatal("installed binary differs from the served engine")
	}
	want := domain.Status{State: domain.StateReady, BytesDone: int64(len(engine)), BytesTotal: int64(len(engine)), Version: "2.1.328", UpdatedAt: r.clock.Now()}
	if diff := cmp.Diff(want, st); diff != "" {
		t.Fatalf("status mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]domain.State{domain.StateQueued, domain.StateDownloading, domain.StateVerifying, domain.StateReady}, r.sink.States()); diff != "" {
		t.Fatalf("saved states mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"https://downloads.example/rel/2.1.328/linux-x64/claude"}, r.source.URLs()); diff != "" {
		t.Fatalf("download urls mismatch (-want +got):\n%s", diff)
	}
}

func TestEnsure_ReportsProgressWhileDownloading(t *testing.T) {
	t.Parallel()
	r := newRig(true)

	if _, err := r.fetcher(t).Ensure(t.Context()); err != nil {
		t.Fatalf("Ensure() error = %v", err)
	}

	var done []int64
	for _, s := range r.sink.All() {
		if s.State == domain.StateDownloading {
			done = append(done, s.BytesDone)
		}
	}
	if len(done) < 3 {
		t.Fatalf("downloading statuses = %v, want progress every 4096 bytes", done)
	}
	for i := 1; i < len(done); i++ {
		if done[i] <= done[i-1] {
			t.Fatalf("progress did not rise: %v", done)
		}
	}
}

func TestEnsure_IsIdempotentOnceInstalled(t *testing.T) {
	t.Parallel()
	r := newRig(true)
	f := r.fetcher(t)
	if _, err := f.Ensure(t.Context()); err != nil {
		t.Fatalf("first Ensure() error = %v", err)
	}

	st, err := f.Ensure(t.Context())

	if err != nil || st.State != domain.StateReady {
		t.Fatalf("second Ensure() = %q, %v, want ready", st.State, err)
	}
	if n := len(r.source.URLs()); n != 1 {
		t.Fatalf("download requests = %d, want 1 in total", n)
	}
}

func TestEnsure_DoesNotDownloadAgainAfterARestart(t *testing.T) {
	t.Parallel()
	r := newRig(true)
	r.disk.Put(r.pin, engine) // installed by an earlier boot, on the volume

	st, err := r.fetcher(t).Ensure(t.Context())

	if err != nil || st.State != domain.StateReady {
		t.Fatalf("Ensure() = %q, %v, want ready", st.State, err)
	}
	if got := r.source.URLs(); len(got) != 0 {
		t.Fatalf("download requests = %v, want none", got)
	}
}

func TestEnsure_ReplacesAnInstalledBinaryThatFailsItsChecksum(t *testing.T) {
	t.Parallel()
	r := newRig(true)
	r.disk.Put(r.pin, []byte("tampered"))

	st, err := r.fetcher(t).Ensure(t.Context())

	if err != nil || st.State != domain.StateReady {
		t.Fatalf("Ensure() = %q, %v, want ready", st.State, err)
	}
	if !bytes.Equal(r.disk.InstalledBytes(r.pin), engine) {
		t.Fatal("tampered binary was kept")
	}
}

func TestEnsure_ResumesAPartialDownload(t *testing.T) {
	t.Parallel()
	r := newRig(true)
	r.disk.PutPart(r.pin, engine[:5000])

	if _, err := r.fetcher(t).Ensure(t.Context()); err != nil {
		t.Fatalf("Ensure() error = %v", err)
	}

	if diff := cmp.Diff([]int64{5000}, r.source.Offsets()); diff != "" {
		t.Fatalf("request offsets mismatch (-want +got):\n%s", diff)
	}
	if !bytes.Equal(r.disk.InstalledBytes(r.pin), engine) {
		t.Fatal("resumed binary differs from the served engine")
	}
}

func TestEnsure_ContinuesWhereABrokenConnectionStopped(t *testing.T) {
	t.Parallel()
	r := newRig(true)
	r.source.CutAfter = []int{6000}

	if _, err := r.fetcher(t).Ensure(t.Context()); err != nil {
		t.Fatalf("Ensure() error = %v", err)
	}

	if diff := cmp.Diff([]int64{0, 6000}, r.source.Offsets()); diff != "" {
		t.Fatalf("request offsets mismatch (-want +got):\n%s", diff)
	}
	if !bytes.Equal(r.disk.InstalledBytes(r.pin), engine) {
		t.Fatal("binary differs from the served engine")
	}
}

func TestEnsure_StartsOverWhenTheServerIgnoresTheRange(t *testing.T) {
	t.Parallel()
	r := newRig(true)
	r.disk.PutPart(r.pin, engine[:5000])
	r.source.IgnoreRange = true

	if _, err := r.fetcher(t).Ensure(t.Context()); err != nil {
		t.Fatalf("Ensure() error = %v", err)
	}

	if !bytes.Equal(r.disk.InstalledBytes(r.pin), engine) {
		t.Fatal("binary differs from the served engine")
	}
}

func TestEnsure_StartsOverWhenTheServerRefusesToResume(t *testing.T) {
	t.Parallel()
	r := newRig(true)
	r.disk.PutPart(r.pin, engine[:5000])
	r.source.RefuseRange = true

	if _, err := r.fetcher(t).Ensure(t.Context()); err != nil {
		t.Fatalf("Ensure() error = %v", err)
	}

	if diff := cmp.Diff([]int64{5000, 0}, r.source.Offsets()); diff != "" {
		t.Fatalf("request offsets mismatch (-want +got):\n%s", diff)
	}
	if !bytes.Equal(r.disk.InstalledBytes(r.pin), engine) {
		t.Fatal("binary differs from the served engine")
	}
}

func TestEnsure_InstallsACompletePartWithoutDownloading(t *testing.T) {
	t.Parallel()
	r := newRig(true)
	r.disk.PutPart(r.pin, engine) // the last boot stopped between download and install

	st, err := r.fetcher(t).Ensure(t.Context())

	if err != nil || st.State != domain.StateReady {
		t.Fatalf("Ensure() = %q, %v, want ready", st.State, err)
	}
	if got := r.source.URLs(); len(got) != 0 {
		t.Fatalf("download requests = %v, want none", got)
	}
}

func TestEnsure_StartsOverWhenThePartIsLargerThanPinned(t *testing.T) {
	t.Parallel()
	r := newRig(true)
	r.disk.PutPart(r.pin, append(append([]byte(nil), engine...), "junk"...))

	if _, err := r.fetcher(t).Ensure(t.Context()); err != nil {
		t.Fatalf("Ensure() error = %v", err)
	}

	if diff := cmp.Diff([]int64{0}, r.source.Offsets()); diff != "" {
		t.Fatalf("request offsets mismatch (-want +got):\n%s", diff)
	}
	if !bytes.Equal(r.disk.InstalledBytes(r.pin), engine) {
		t.Fatal("binary differs from the served engine")
	}
}

func TestEnsure_FailsWhenTheServerSendsLessThanPinned(t *testing.T) {
	t.Parallel()
	r := newRig(true)
	r.source.Data = engine[:10000]

	st, err := r.fetcher(t).Ensure(t.Context())

	if err == nil || st.State != domain.StateFailed || !strings.Contains(st.Error, "ended before the whole engine arrived") {
		t.Fatalf("Ensure() = %+v, %v, want failed with a plain short-download reason", st, err)
	}
}

func TestEnsure_DeletesADownloadThatFailsItsChecksum(t *testing.T) {
	t.Parallel()
	r := newRig(true)
	bad := append([]byte(nil), engine...)
	bad[100] ^= 0xff
	r.source.Data = bad

	st, err := r.fetcher(t).Ensure(t.Context())

	if !errors.Is(err, app.ErrChecksum) {
		t.Fatalf("Ensure() error = %v, want %v", err, app.ErrChecksum)
	}
	if st.State != domain.StateFailed || !strings.Contains(st.Error, "did not match the checksum") {
		t.Fatalf("status = %+v, want failed with a plain checksum reason", st)
	}
	if r.disk.HasPart(r.pin) || r.disk.InstalledBytes(r.pin) != nil {
		t.Fatal("a download with a bad checksum was kept")
	}
}

// A part that cannot be deleted would fail the checksum on every later try
// (a complete part is never downloaded again), so the failure must say so.
func TestEnsure_ReportsAMismatchedDownloadThatCannotBeDeleted(t *testing.T) {
	t.Parallel()
	r := newRig(true)
	bad := append([]byte(nil), engine...)
	bad[100] ^= 0xff
	r.source.Data = bad
	errLocked := errors.New("disk is read-only")
	r.disk.FailRemovePart = errLocked

	st, err := r.fetcher(t).Ensure(t.Context())

	if !errors.Is(err, app.ErrChecksum) || !errors.Is(err, errLocked) {
		t.Fatalf("Ensure() error = %v, want both %v and %v", err, app.ErrChecksum, errLocked)
	}
	if st.State != domain.StateFailed || !strings.Contains(st.Error, "could not be deleted") {
		t.Fatalf("status = %+v, want failed with a reason that says the bad download stays", st)
	}
}

func TestEnsure_ReportsAnOversizedDownloadThatCannotBeDeleted(t *testing.T) {
	t.Parallel()
	r := newRig(true)
	r.source.Data = append(append([]byte(nil), engine...), "extra"...)
	errLocked := errors.New("disk is read-only")
	r.disk.FailRemovePart = errLocked

	_, err := r.fetcher(t).Ensure(t.Context())

	if !errors.Is(err, app.ErrTooLarge) || !errors.Is(err, errLocked) {
		t.Fatalf("Ensure() error = %v, want both %v and %v", err, app.ErrTooLarge, errLocked)
	}
}

func TestEnsure_DeletesADownloadLargerThanPinned(t *testing.T) {
	t.Parallel()
	r := newRig(true)
	r.source.Data = append(append([]byte(nil), engine...), "extra"...)

	st, err := r.fetcher(t).Ensure(t.Context())

	if !errors.Is(err, app.ErrTooLarge) || st.State != domain.StateFailed {
		t.Fatalf("Ensure() = %q, %v, want failed with %v", st.State, err, app.ErrTooLarge)
	}
	if r.disk.HasPart(r.pin) || r.disk.InstalledBytes(r.pin) != nil {
		t.Fatal("an oversized download was kept")
	}
}

func TestEnsure_FailsWithAPlainReasonAfterTheLastAttempt(t *testing.T) {
	t.Parallel()
	r := newRig(true)
	r.source.CutAfter = []int{1000, 1000, 1000}

	st, err := r.fetcher(t).Ensure(t.Context())

	if err == nil || st.State != domain.StateFailed {
		t.Fatalf("Ensure() = %q, %v, want failed", st.State, err)
	}
	if !strings.Contains(st.Error, "The engine download stopped") || !strings.Contains(st.Error, "continues from where it stopped") {
		t.Fatalf("status error = %q, want a plain reason", st.Error)
	}
	if size, _ := r.disk.PartSize(r.pin); size != 3000 {
		t.Fatalf("part size = %d, want 3000 kept for the next try", size)
	}
	if diff := cmp.Diff([]int64{0, 1000, 2000}, r.source.Offsets()); diff != "" {
		t.Fatalf("request offsets mismatch (-want +got):\n%s", diff)
	}
}

func TestEnsure_RetryAfterAFailureFinishesTheDownload(t *testing.T) {
	t.Parallel()
	r := newRig(true)
	r.source.FailNext = []error{errors.New("no route to host"), errors.New("no route to host"), errors.New("no route to host")}
	f := r.fetcher(t)
	if _, err := f.Ensure(t.Context()); err == nil {
		t.Fatal("first Ensure() succeeded, want a failure")
	}

	st, err := f.Ensure(t.Context())

	if err != nil || st.State != domain.StateReady {
		t.Fatalf("second Ensure() = %q, %v, want ready", st.State, err)
	}
}

func TestEnsure_FailsOnAPlatformWithNoPinnedEngine(t *testing.T) {
	t.Parallel()
	r := newRig(true)
	r.goarch = "riscv64"

	st, err := r.fetcher(t).Ensure(t.Context())

	if !errors.Is(err, domain.ErrUnsupportedPlatform) || st.State != domain.StateFailed {
		t.Fatalf("Ensure() = %q, %v, want failed with %v", st.State, err, domain.ErrUnsupportedPlatform)
	}
	if !strings.Contains(st.Error, "linux/riscv64") {
		t.Fatalf("status error = %q, want the platform named", st.Error)
	}
	if got := r.source.URLs(); len(got) != 0 {
		t.Fatalf("download requests = %v, want none", got)
	}
}

func TestEnsure_FailsWhenTheEngineCannotBeSaved(t *testing.T) {
	t.Parallel()
	r := newRig(true)
	r.disk.FailInstall = errors.New("read-only file system")

	st, err := r.fetcher(t).Ensure(t.Context())

	if err == nil || st.State != domain.StateFailed || !strings.Contains(st.Error, "could not be saved") {
		t.Fatalf("Ensure() = %+v, %v, want failed with a plain save reason", st, err)
	}
}

func TestEnsure_StopsWhenTheLicenceCannotBeChecked(t *testing.T) {
	t.Parallel()
	r := newRig(true)
	r.gate.Fail(errors.New("disk I/O error"))

	_, err := r.fetcher(t).Ensure(t.Context())

	if err == nil {
		t.Fatal("Ensure() succeeded, want an error")
	}
	if got := r.source.URLs(); len(got) != 0 {
		t.Fatalf("download requests = %v, want none", got)
	}
}

func TestEnsure_StopsWhenCancelled(t *testing.T) {
	t.Parallel()
	r := newRig(true)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := r.fetcher(t).Ensure(ctx)

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Ensure() error = %v, want %v", err, context.Canceled)
	}
	if r.disk.InstalledBytes(r.pin) != nil {
		t.Fatal("engine installed after cancel")
	}
}

func TestEnsure_ReportsAFailingStatusStore(t *testing.T) {
	t.Parallel()
	r := newRig(true)
	r.sink.Fail(errors.New("database is locked"))

	_, err := r.fetcher(t).Ensure(t.Context())

	if err == nil || !strings.Contains(err.Error(), "database is locked") {
		t.Fatalf("Ensure() error = %v, want the status store error", err)
	}
}

func TestEnsure_RunsOneDownloadForConcurrentCallers(t *testing.T) {
	t.Parallel()
	r := newRig(true)
	f := r.fetcher(t)
	var wg sync.WaitGroup
	states := make([]domain.State, 4)
	for i := range states {
		wg.Go(func() {
			st, _ := f.Ensure(t.Context())
			states[i] = st.State
		})
	}
	wg.Wait()

	for i, s := range states {
		if s != domain.StateReady {
			t.Fatalf("caller %d state = %q, want ready", i, s)
		}
	}
	if n := len(r.source.URLs()); n != 1 {
		t.Fatalf("download requests = %d, want 1", n)
	}
}

func TestNew_RejectsAnInvalidPin(t *testing.T) {
	t.Parallel()
	r := newRig(true)
	r.pin.Version = "latest"
	_, err := app.New(app.Deps{Pin: r.pin, GOOS: "linux", GOARCH: "amd64", Licence: r.gate, Status: r.sink, Source: r.source, Disk: r.disk, Clock: r.clock})
	if !errors.Is(err, domain.ErrInvalidPin) {
		t.Fatalf("New() error = %v, want %v", err, domain.ErrInvalidPin)
	}
}

func TestNew_RejectsMissingPorts(t *testing.T) {
	t.Parallel()
	r := newRig(true)
	_, err := app.New(app.Deps{Pin: r.pin, GOOS: "linux", GOARCH: "amd64"})
	if err == nil {
		t.Fatal("New() without ports succeeded")
	}
}
