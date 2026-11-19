// SPDX-License-Identifier: MIT

// Package enginefetchtest has in-memory fakes for the engine fetch ports and
// the shared contract suite every Disk must pass.
package enginefetchtest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"sync"

	"github.com/SDK-E/agentsone/internal/enginefetch/app"
	"github.com/SDK-E/agentsone/internal/enginefetch/domain"
)

// Gate is a licence gate with a fixed answer.
type Gate struct {
	mu       sync.Mutex
	accepted bool
	err      error
}

// NewGate returns a gate that answers accepted.
func NewGate(accepted bool) *Gate { return &Gate{accepted: accepted} }

// Accept records the licence as accepted.
func (g *Gate) Accept() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.accepted = true
}

// Fail makes every check fail with err.
func (g *Gate) Fail(err error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.err = err
}

// Accepted implements app.LicenceGate.
func (g *Gate) Accepted(context.Context) (bool, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.accepted, g.err
}

// Sink records every status it is given.
type Sink struct {
	mu   sync.Mutex
	all  []domain.Status
	fail error
}

// SaveStatus implements app.StatusSink.
func (s *Sink) SaveStatus(_ context.Context, st domain.Status) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail != nil {
		return s.fail
	}
	s.all = append(s.all, st)
	return nil
}

// Fail makes every save fail with err.
func (s *Sink) Fail(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fail = err
}

// All returns the saved statuses in order.
func (s *Sink) All() []domain.Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]domain.Status(nil), s.all...)
}

// States returns the states of the saved statuses, with repeats folded.
func (s *Sink) States() []domain.State {
	var out []domain.State
	for _, st := range s.All() {
		if len(out) == 0 || out[len(out)-1] != st.State {
			out = append(out, st.State)
		}
	}
	return out
}

// Last returns the last saved status.
func (s *Sink) Last() domain.Status {
	all := s.All()
	if len(all) == 0 {
		return domain.Status{}
	}
	return all[len(all)-1]
}

// Source serves one engine file from memory, with byte ranges.
type Source struct {
	mu sync.Mutex
	// Data is the file the source serves.
	Data []byte
	// IgnoreRange answers every request from byte 0, as a server without
	// range support does.
	IgnoreRange bool
	// RefuseRange answers a ranged request with app.ErrRangeRefused.
	RefuseRange bool
	// CutAfter ends the next responses with an error after that many bytes,
	// once per entry.
	CutAfter []int
	// FailNext makes the next Fetch calls fail with these errors, once each.
	FailNext []error
	offsets  []int64
	urls     []string
}

// Fetch implements app.Source.
func (s *Source) Fetch(ctx context.Context, url string, offset int64) (app.Body, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return app.Body{}, err //nolint:wrapcheck // the context error passes through
	}
	s.offsets = append(s.offsets, offset)
	s.urls = append(s.urls, url)
	if len(s.FailNext) > 0 {
		err := s.FailNext[0]
		s.FailNext = s.FailNext[1:]
		return app.Body{}, err
	}
	if offset > 0 && s.RefuseRange {
		return app.Body{}, app.ErrRangeRefused
	}
	start := offset
	if s.IgnoreRange {
		start = 0
	}
	if start > int64(len(s.Data)) {
		return app.Body{}, app.ErrRangeRefused
	}
	var r io.Reader = bytes.NewReader(s.Data[start:])
	if len(s.CutAfter) > 0 {
		r = &cutReader{r: r, left: s.CutAfter[0]}
		s.CutAfter = s.CutAfter[1:]
	}
	return app.Body{ReadCloser: io.NopCloser(r), Offset: start}, nil
}

// Offsets returns the offset of every request so far.
func (s *Source) Offsets() []int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]int64(nil), s.offsets...)
}

// URLs returns the URL of every request so far.
func (s *Source) URLs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.urls...)
}

// ErrCut is the error a cut response ends with.
var ErrCut = errors.New("connection reset by peer")

type cutReader struct {
	r    io.Reader
	left int
}

func (c *cutReader) Read(p []byte) (int, error) {
	if c.left <= 0 {
		return 0, ErrCut
	}
	if len(p) > c.left {
		p = p[:c.left]
	}
	n, err := c.r.Read(p)
	c.left -= n
	return n, err //nolint:wrapcheck // reader errors pass through
}

// Disk keeps the part file and the installed binary in memory.
type Disk struct {
	mu        sync.Mutex
	part      map[string][]byte
	installed map[string][]byte
	// FailInstall makes Install fail with this error.
	FailInstall error
}

// NewDisk returns an empty disk.
func NewDisk() *Disk {
	return &Disk{part: map[string][]byte{}, installed: map[string][]byte{}}
}

func key(p domain.Pin) string { return p.Engine + "/" + p.Version }

// Sum is the lower-case hex sha256 of b.
func Sum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// Installed implements app.Disk.
func (d *Disk) Installed(p domain.Pin) (string, int64, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	b, ok := d.installed[key(p)]
	if !ok {
		return "", 0, fs.ErrNotExist
	}
	return Sum(b), int64(len(b)), nil
}

// Put installs b directly, as if an earlier run had installed it.
func (d *Disk) Put(p domain.Pin, b []byte) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.installed[key(p)] = append([]byte(nil), b...)
}

// PutPart leaves b as a partial download.
func (d *Disk) PutPart(p domain.Pin, b []byte) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.part[key(p)] = append([]byte(nil), b...)
}

// InstalledBytes returns the installed binary, or nil.
func (d *Disk) InstalledBytes(p domain.Pin) []byte {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.installed[key(p)]
}

// HasPart reports whether a partial download is left.
func (d *Disk) HasPart(p domain.Pin) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, ok := d.part[key(p)]
	return ok
}

// RemoveInstalled implements app.Disk.
func (d *Disk) RemoveInstalled(p domain.Pin) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.installed, key(p))
	return nil
}

// PartSize implements app.Disk.
func (d *Disk) PartSize(p domain.Pin) (int64, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return int64(len(d.part[key(p)])), nil
}

// AppendPart implements app.Disk.
func (d *Disk) AppendPart(p domain.Pin, offset int64) (io.WriteCloser, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	cur := d.part[key(p)]
	if offset > int64(len(cur)) {
		return nil, fmt.Errorf("offset %d is past the part size %d", offset, len(cur))
	}
	d.part[key(p)] = cur[:offset]
	return &partWriter{d: d, k: key(p)}, nil
}

type partWriter struct {
	d *Disk
	k string
}

func (w *partWriter) Write(b []byte) (int, error) {
	w.d.mu.Lock()
	defer w.d.mu.Unlock()
	w.d.part[w.k] = append(w.d.part[w.k], b...)
	return len(b), nil
}

func (w *partWriter) Close() error { return nil }

// PartSum implements app.Disk.
func (d *Disk) PartSum(p domain.Pin) (string, int64, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	b, ok := d.part[key(p)]
	if !ok {
		return "", 0, fs.ErrNotExist
	}
	return Sum(b), int64(len(b)), nil
}

// RemovePart implements app.Disk.
func (d *Disk) RemovePart(p domain.Pin) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.part, key(p))
	return nil
}

// Install implements app.Disk.
func (d *Disk) Install(p domain.Pin) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.FailInstall != nil {
		return d.FailInstall
	}
	b, ok := d.part[key(p)]
	if !ok {
		return fs.ErrNotExist
	}
	d.installed[key(p)] = b
	delete(d.part, key(p))
	return nil
}
