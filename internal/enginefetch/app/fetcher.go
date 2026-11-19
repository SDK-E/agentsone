// SPDX-License-Identifier: MIT

// Package app is the engine fetch use case: after a human accepted the engine
// licence, download the pinned engine through the edge, resume a download
// that stopped, verify it against the pinned checksum, install it on the
// state volume and report every step to the daemon.
package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/enginefetch/domain"
)

// Errors Ensure returns.
var (
	// ErrLicenceNotAccepted means no human accepted the engine licence yet,
	// so nothing was downloaded.
	ErrLicenceNotAccepted = errors.New("the engine licence is not accepted")
	// ErrChecksum means the download did not match the pinned checksum.
	ErrChecksum = errors.New("the engine download does not match its pinned checksum")
	// ErrTooLarge means the server sent more bytes than the pinned size.
	ErrTooLarge = errors.New("the engine download is larger than its pinned size")
	// ErrRangeRefused is returned by a Source when the server cannot resume
	// at the asked offset; the download then starts over.
	ErrRangeRefused = errors.New("the download server cannot resume at this offset")

	errShort = errors.New("the download ended before the whole engine arrived")
)

// LicenceGate says whether a human accepted the engine licence.
type LicenceGate interface {
	Accepted(ctx context.Context) (bool, error)
}

// StatusSink stores the fetch status the daemon shows.
type StatusSink interface {
	SaveStatus(ctx context.Context, s domain.Status) error
}

// Body is a download response. Offset is the byte of the file the body
// starts at: the asked offset when the server resumed, 0 when it sent the
// whole file.
type Body struct {
	io.ReadCloser
	Offset int64
}

// Source downloads a file from an offset.
type Source interface {
	Fetch(ctx context.Context, url string, offset int64) (Body, error)
}

// Disk holds the partial download and the installed engine.
type Disk interface {
	// Installed hashes the installed binary. It answers fs.ErrNotExist when
	// none is installed.
	Installed(p domain.Pin) (sum string, size int64, err error)
	RemoveInstalled(p domain.Pin) error
	// PartSize is the size of the partial download, 0 when there is none.
	PartSize(p domain.Pin) (int64, error)
	// AppendPart cuts the partial download to offset and writes after it.
	AppendPart(p domain.Pin, offset int64) (io.WriteCloser, error)
	// PartSum hashes the partial download.
	PartSum(p domain.Pin) (sum string, size int64, err error)
	RemovePart(p domain.Pin) error
	// Install moves the partial download into place as an executable.
	Install(p domain.Pin) error
}

// Defaults of Deps.
const (
	DefaultAttempts    = 3
	DefaultReportEvery = 4 << 20
)

// Deps are the ports and settings of the fetcher.
type Deps struct {
	Pin          domain.Pin
	GOOS, GOARCH string
	Licence      LicenceGate
	Status       StatusSink
	Source       Source
	Disk         Disk
	Clock        core.Clock
	// Attempts is how many times one Ensure tries the download before it
	// reports failure. Default DefaultAttempts.
	Attempts int
	// ReportEvery is how many bytes pass between progress reports. Default
	// DefaultReportEvery.
	ReportEvery int64
	// Wait runs between attempts, for a back-off. Default: no wait.
	Wait func(ctx context.Context, attempt int) error
}

// Fetcher installs the pinned engine.
type Fetcher struct {
	d  Deps
	mu sync.Mutex // one Ensure at a time
}

// New checks the pin and the ports.
func New(d Deps) (*Fetcher, error) {
	if err := d.Pin.Validate(); err != nil {
		return nil, err
	}
	if d.Licence == nil || d.Status == nil || d.Source == nil || d.Disk == nil || d.Clock == nil {
		return nil, errors.New("enginefetch: every port is required")
	}
	if d.Attempts <= 0 {
		d.Attempts = DefaultAttempts
	}
	if d.ReportEvery <= 0 {
		d.ReportEvery = DefaultReportEvery
	}
	if d.Wait == nil {
		d.Wait = func(context.Context, int) error { return nil }
	}
	return &Fetcher{d: d}, nil
}

// Ensure makes sure the pinned engine is installed. It downloads nothing
// until the licence is accepted. It is safe to call again at any time: an
// installed engine that passes its checksum is kept, and a partial download
// continues where it stopped. Concurrent calls wait for each other.
func (f *Fetcher) Ensure(ctx context.Context) (domain.Status, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	accepted, err := f.d.Licence.Accepted(ctx)
	if err != nil {
		return domain.Status{}, fmt.Errorf("check the engine licence: %w", err)
	}
	if !accepted {
		st := f.status(domain.StateWaitingLicence, 0, 0, "")
		return st, f.save(ctx, st, ErrLicenceNotAccepted)
	}
	a, err := f.d.Pin.For(f.d.GOOS, f.d.GOARCH)
	if err != nil {
		reason := fmt.Sprintf("No Claude engine is pinned for this machine (%s/%s).", f.d.GOOS, f.d.GOARCH)
		return f.fail(ctx, 0, 0, reason, err)
	}
	if f.installed(a) {
		st := f.status(domain.StateReady, a.Size, a.Size, "")
		return st, f.save(ctx, st, nil)
	}
	if err := f.save(ctx, f.status(domain.StateQueued, 0, a.Size, ""), nil); err != nil {
		return domain.Status{}, err
	}
	if err := f.download(ctx, a); err != nil {
		return f.downloadFailed(ctx, a, err)
	}
	if err := f.save(ctx, f.status(domain.StateVerifying, a.Size, a.Size, ""), nil); err != nil {
		return domain.Status{}, err
	}
	sum, size, err := f.d.Disk.PartSum(f.d.Pin)
	if err != nil {
		return f.fail(ctx, 0, a.Size, "The engine download could not be read back: "+err.Error()+".", err)
	}
	if sum != a.SHA256 || size != a.Size {
		_ = f.d.Disk.RemovePart(f.d.Pin) // a failed removal is retried by the next download, which truncates
		reason := "The downloaded engine did not match the checksum pinned in this release, so it was deleted. " +
			"Try again later; if it fails again, the download source changed and needs a new release."
		return f.fail(ctx, 0, a.Size, reason, ErrChecksum)
	}
	if err := f.d.Disk.Install(f.d.Pin); err != nil {
		return f.fail(ctx, a.Size, a.Size, "The engine could not be saved on the state volume: "+err.Error()+".", err)
	}
	st := f.status(domain.StateReady, a.Size, a.Size, "")
	return st, f.save(ctx, st, nil)
}

// installed reports whether a binary that passes the checksum is in place.
// A binary that fails it is removed so it is downloaded again.
func (f *Fetcher) installed(a domain.Artifact) bool {
	sum, size, err := f.d.Disk.Installed(f.d.Pin)
	if err != nil {
		return false
	}
	if sum == a.SHA256 && size == a.Size {
		return true
	}
	_ = f.d.Disk.RemoveInstalled(f.d.Pin) // install renames over it anyway
	return false
}

func (f *Fetcher) download(ctx context.Context, a domain.Artifact) error {
	for attempt := 1; ; attempt++ {
		err := f.attempt(ctx, a)
		switch {
		case err == nil:
			return nil
		case ctx.Err() != nil:
			return ctx.Err()
		case errors.Is(err, ErrTooLarge), attempt >= f.d.Attempts:
			return err
		}
		if werr := f.d.Wait(ctx, attempt); werr != nil {
			return werr
		}
	}
}

// attempt downloads what is missing of the part file once.
func (f *Fetcher) attempt(ctx context.Context, a domain.Artifact) error {
	offset, err := f.d.Disk.PartSize(f.d.Pin)
	if err != nil {
		return fmt.Errorf("read the partial download: %w", err)
	}
	if offset > a.Size {
		if err := f.d.Disk.RemovePart(f.d.Pin); err != nil {
			return fmt.Errorf("remove an oversized partial download: %w", err)
		}
		offset = 0
	}
	if offset == a.Size {
		return nil
	}
	_ = f.save(ctx, f.status(domain.StateDownloading, offset, a.Size, ""), nil) // progress is best effort
	url := f.d.Pin.URL(a)
	body, err := f.d.Source.Fetch(ctx, url, offset)
	if errors.Is(err, ErrRangeRefused) {
		offset = 0
		body, err = f.d.Source.Fetch(ctx, url, 0)
	}
	if err != nil {
		return err
	}
	defer func() { _ = body.Close() }() // a read-only body has nothing to flush
	if body.Offset != offset {
		if body.Offset != 0 {
			return fmt.Errorf("the download server resumed at byte %d, not %d", body.Offset, offset)
		}
		offset = 0
	}
	w, err := f.d.Disk.AppendPart(f.d.Pin, offset)
	if err != nil {
		return fmt.Errorf("open the partial download: %w", err)
	}
	pw := &progress{f: f, ctx: ctx, w: w, done: offset, total: a.Size, next: offset + f.d.ReportEvery}
	// Copy in pieces no larger than the report step, so progress shows even
	// when the body could write itself in one go.
	buf := make([]byte, min(32<<10, f.d.ReportEvery))
	n, cerr := io.CopyBuffer(pw, struct{ io.Reader }{io.LimitReader(body, a.Size-offset+1)}, buf)
	if err := w.Close(); err != nil && cerr == nil {
		cerr = fmt.Errorf("save the partial download: %w", err)
	}
	switch {
	case offset+n > a.Size:
		_ = f.d.Disk.RemovePart(f.d.Pin) // the next download truncates it anyway
		return ErrTooLarge
	case cerr != nil:
		return cerr
	case offset+n < a.Size:
		return errShort
	}
	return nil
}

func (f *Fetcher) downloadFailed(ctx context.Context, a domain.Artifact, err error) (domain.Status, error) {
	if ctx.Err() != nil {
		return domain.Status{}, err
	}
	done, _ := f.d.Disk.PartSize(f.d.Pin) // only shown as progress
	if errors.Is(err, ErrTooLarge) {
		return f.fail(ctx, 0, a.Size, "The download was larger than the pinned engine, so it was deleted.", err)
	}
	reason := "The engine download stopped: " + err.Error() + ". It continues from where it stopped on the next try."
	return f.fail(ctx, done, a.Size, reason, err)
}

func (f *Fetcher) fail(ctx context.Context, done, total int64, reason string, cause error) (domain.Status, error) {
	st := f.status(domain.StateFailed, done, total, reason)
	return st, f.save(ctx, st, cause)
}

func (f *Fetcher) status(state domain.State, done, total int64, reason string) domain.Status {
	return domain.Status{
		State: state, BytesDone: done, BytesTotal: total, Version: f.d.Pin.Version,
		Error: reason, UpdatedAt: f.d.Clock.Now(),
	}
}

// save stores st and returns cause, or the store error when there is no cause.
func (f *Fetcher) save(ctx context.Context, st domain.Status, cause error) error {
	err := f.d.Status.SaveStatus(context.WithoutCancel(ctx), st)
	switch {
	case cause != nil:
		return cause
	case err != nil:
		return fmt.Errorf("save the engine fetch status: %w", err)
	}
	return nil
}

// progress counts written bytes and reports them every ReportEvery bytes.
type progress struct {
	f     *Fetcher
	ctx   context.Context
	w     io.Writer
	done  int64
	total int64
	next  int64
}

func (p *progress) Write(b []byte) (int, error) {
	n, err := p.w.Write(b)
	p.done += int64(n)
	if p.done >= p.next && p.done < p.total {
		p.next = p.done + p.f.d.ReportEvery
		_ = p.f.save(p.ctx, p.f.status(domain.StateDownloading, p.done, p.total, ""), nil) // best effort
	}
	return n, err
}
