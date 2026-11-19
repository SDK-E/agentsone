// SPDX-License-Identifier: MIT

package app_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/machine/app"
	"github.com/SDK-E/agentsone/internal/machine/domain"
)

// fakeApt is an in-memory apt: installed packages and the manual set.
type fakeApt struct {
	mu        sync.Mutex
	installed map[string]bool
	manual    map[string]bool
	broken    map[string]string // name -> install error text
	calls     []string
}

func newFakeApt(baseline ...string) *fakeApt {
	a := &fakeApt{installed: map[string]bool{}, manual: map[string]bool{}, broken: map[string]string{}}
	for _, n := range baseline {
		a.installed[n], a.manual[n] = true, true
	}
	return a
}

func (a *fakeApt) Installed(context.Context) ([]domain.Package, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []domain.Package
	for n := range a.installed {
		out = append(out, domain.Package{Name: n, Version: "1.0", Architecture: "arm64", Summary: n + " tool"})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (a *fakeApt) Manual(context.Context) ([]string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []string
	for n := range a.manual {
		out = append(out, n)
	}
	sort.Strings(out)
	return out, nil
}

func (a *fakeApt) Install(_ context.Context, name, version string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls = append(a.calls, "install "+name+"="+version)
	if reason, bad := a.broken[name]; bad {
		return errors.New(reason)
	}
	a.installed[name], a.manual[name] = true, true
	return nil
}

func (a *fakeApt) Remove(_ context.Context, name string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls = append(a.calls, "remove "+name)
	delete(a.installed, name)
	delete(a.manual, name)
	return nil
}

func (a *fakeApt) Calls() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return slices.Clone(a.calls)
}

// fakeFile is one file in fakeFS.
type fakeFile struct {
	data     []byte
	mode     fs.FileMode
	uid, gid int
	dir      bool
}

// fakeFS is an in-memory file system keyed by absolute path.
type fakeFS struct {
	mu    sync.Mutex
	files map[string]*fakeFile
	fail  error
}

func newFakeFS() *fakeFS {
	return &fakeFS{files: map[string]*fakeFile{"/": {dir: true, mode: 0o755}}}
}

func (f *fakeFS) put(p string, data string) {
	f.files[p] = &fakeFile{data: []byte(data), mode: 0o644}
	for d := path.Dir(p); d != "/"; d = path.Dir(d) {
		f.files[d] = &fakeFile{dir: true, mode: 0o755}
	}
}

func (f *fakeFS) get(p string) (*fakeFile, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	file, ok := f.files[p]
	return file, ok
}

func (f *fakeFS) List(_ context.Context, a domain.Area) ([]domain.FileEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	dir, ok := f.files[a.Path]
	if !ok {
		return nil, fmt.Errorf("%s: %w", a.Path, core.ErrNotFound)
	}
	if !dir.dir {
		return nil, fmt.Errorf("%s is a file: %w", a.Path, core.ErrInvalid)
	}
	var out []domain.FileEntry
	for p, file := range f.files {
		if p != a.Path && path.Dir(p) == a.Path {
			out = append(out, entry(p, file))
		}
	}
	return out, nil
}

func entry(p string, file *fakeFile) domain.FileEntry {
	t := domain.FileRegular
	if file.dir {
		t = domain.FileDir
	}
	return domain.FileEntry{Path: p, Name: path.Base(p), Type: t, Size: int64(len(file.data)), Mode: file.mode}
}

func (f *fakeFS) Read(_ context.Context, a domain.Area, limit int64) ([]byte, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	file, ok := f.files[a.Path]
	if !ok {
		return nil, 0, fmt.Errorf("%s: %w", a.Path, core.ErrNotFound)
	}
	data := file.data
	if int64(len(data)) > limit {
		data = data[:limit]
	}
	return slices.Clone(data), int64(len(file.data)), nil
}

func (f *fakeFS) Write(_ context.Context, a domain.Area, w app.FileWrite) (domain.FileEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		return domain.FileEntry{}, f.fail
	}
	parent := path.Dir(a.Path)
	if _, ok := f.files[parent]; !ok {
		if !w.CreateParents {
			return domain.FileEntry{}, fmt.Errorf("%s: %w", parent, core.ErrNotFound)
		}
		for d := parent; d != "/"; d = path.Dir(d) {
			f.files[d] = &fakeFile{dir: true, mode: 0o755, uid: w.UID, gid: w.GID}
		}
	}
	file := &fakeFile{data: slices.Clone(w.Data), mode: w.Mode, uid: w.UID, gid: w.GID}
	f.files[a.Path] = file
	return entry(a.Path, file), nil
}

func (f *fakeFS) Delete(_ context.Context, a domain.Area, recursive bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	file, ok := f.files[a.Path]
	if !ok {
		return fmt.Errorf("%s: %w", a.Path, core.ErrNotFound)
	}
	if file.dir && !recursive {
		return fmt.Errorf("%s is a folder: %w", a.Path, core.ErrConflict)
	}
	for p := range f.files {
		if p == a.Path || strings.HasPrefix(p, a.Path+"/") {
			delete(f.files, p)
		}
	}
	return nil
}

// fakeProcs is a process table.
type fakeProcs struct {
	mu      sync.Mutex
	procs   []domain.Process
	signals []string
}

func (p *fakeProcs) List(context.Context) ([]domain.Process, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.procs), nil
}

func (p *fakeProcs) Get(_ context.Context, pid int) (domain.Process, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, proc := range p.procs {
		if proc.PID == pid {
			return proc, nil
		}
	}
	return domain.Process{}, fmt.Errorf("pid %d: %w", pid, core.ErrNotFound)
}

func (p *fakeProcs) Signal(_ context.Context, pid int, s domain.Signal) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.signals = append(p.signals, fmt.Sprintf("%s %d", s, pid))
	return nil
}

type fakeUsers []domain.MachineUser

func (u fakeUsers) List(context.Context) ([]domain.MachineUser, error) { return u, nil }

type fakeMetrics struct{ m domain.Metrics }

func (f fakeMetrics) Read(context.Context) (domain.Metrics, error) { return f.m, nil }

// fakeRecipes stores the recipe and content blobs in memory.
type fakeRecipes struct {
	mu     sync.Mutex
	recipe *domain.Recipe
	blobs  map[string][]byte
	saves  int
}

func newFakeRecipes() *fakeRecipes { return &fakeRecipes{blobs: map[string][]byte{}} }

func (s *fakeRecipes) Load(context.Context) (domain.Recipe, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.recipe == nil {
		return domain.Recipe{}, false, nil
	}
	r := *s.recipe
	r.Packages = slices.Clone(r.Packages)
	r.Files = slices.Clone(r.Files)
	return r, true, nil
}

func (s *fakeRecipes) Save(_ context.Context, r domain.Recipe) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.recipe = &r
	s.saves++
	return nil
}

func (s *fakeRecipes) PutBlob(_ context.Context, data []byte) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sum := sha256.Sum256(data)
	h := hex.EncodeToString(sum[:])
	s.blobs[h] = slices.Clone(data)
	return h, nil
}

func (s *fakeRecipes) Blob(_ context.Context, h string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.blobs[h]
	if !ok {
		return nil, fmt.Errorf("blob %s: %w", h, core.ErrNotFound)
	}
	return b, nil
}

func (s *fakeRecipes) Current() domain.Recipe {
	r, _, _ := s.Load(context.Background())
	return r
}

// fakeBaseline is the image's package list, or none.
type fakeBaseline struct {
	list []string
	ok   bool
}

func (b fakeBaseline) Baseline(context.Context) ([]string, bool, error) { return b.list, b.ok, nil }

// fakePower records exits and fires timers by hand.
type fakePower struct {
	mu     sync.Mutex
	exits  []int
	timers []timer
}

type timer struct {
	d time.Duration
	f func()
}

func (p *fakePower) Exit(code int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.exits = append(p.exits, code)
}

func (p *fakePower) AfterFunc(d time.Duration, f func()) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.timers = append(p.timers, timer{d, f})
}

func (p *fakePower) Fire() {
	p.mu.Lock()
	ts := p.timers
	p.timers = nil
	p.mu.Unlock()
	for _, t := range ts {
		t.f()
	}
}

func (p *fakePower) Exits() []int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.exits)
}

// failingAudit refuses every append.
type failingAudit struct{}

func (failingAudit) Append(context.Context, core.AuditEntry) error {
	return errors.New("audit disk full")
}
