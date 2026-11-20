// SPDX-License-Identifier: MIT

// Package files is the built-in files event source. It scans the folders it
// is told to watch and publishes agentsone.files.created, .modified and
// .deleted when a file appears, changes or goes away.
//
// The first scan of a folder is the baseline: files that are already there
// are not announced. A new or changed file is announced only when two scans
// in a row see it unchanged, so a file that is still being written is
// announced once, when the writer is done. A delete is announced at once.
// A scan that fails keeps what is known, so an unreadable folder is never
// reported as a mass delete. A file created and removed between two settled
// scans is never announced. Events are at-least-once: after a restart the
// baseline is taken again, so a change made while the daemon was down is not
// reported.
package files

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"
	"sort"
	"time"

	"github.com/SDK-E/agentsone/internal/emitkit/app"
	"github.com/SDK-E/agentsone/internal/emitkit/domain"
)

// Entry is what a scan knows about one file.
type Entry struct {
	Size    int64
	ModTime time.Time
}

// Walker lists the regular files below a watched folder, keyed by path. It
// returns ErrTooManyFiles when there are more than limit.
type Walker interface {
	Walk(watch string, limit int) (map[string]Entry, error)
}

// ErrTooManyFiles means a watched folder holds more files than the source
// will track.
var ErrTooManyFiles = errors.New("too many files under the watched folder")

// Config is how the source behaves.
type Config struct {
	// Watches are the folders to watch, as absolute paths.
	Watches []string
	// Interval is the time between scans.
	Interval time.Duration
	// MaxFiles is the most files tracked under one watch.
	MaxFiles int
}

// DefaultConfig is what the machine runs with. There are no watches until the
// owner names some.
func DefaultConfig() Config {
	return Config{Interval: 10 * time.Second, MaxFiles: 10000}
}

type watchState struct {
	baselined bool
	known     map[string]Entry // what listeners have been told
	pending   map[string]Entry // what the last scan saw and is not told yet
	lastErr   string
}

// Source is the files source.
type Source struct {
	cfg    Config
	walker Walker
	state  map[string]*watchState
}

var _ app.Source = (*Source)(nil)

// New returns a source over w.
func New(cfg Config, w Walker) *Source {
	return &Source{cfg: cfg, walker: w, state: map[string]*watchState{}}
}

// Run scans until ctx is cancelled.
func (s *Source) Run(ctx context.Context, d app.Deps) error {
	return app.Every(ctx, d.Sleeper, s.cfg.Interval, d.Log, func(ctx context.Context) error {
		for _, w := range s.cfg.Watches {
			s.scan(ctx, d, w)
		}
		return nil
	})
}

func (s *Source) scan(ctx context.Context, d app.Deps, watch string) {
	st := s.state[watch]
	if st == nil {
		st = &watchState{known: map[string]Entry{}, pending: map[string]Entry{}}
		s.state[watch] = st
	}
	cur, err := s.walker.Walk(watch, s.cfg.MaxFiles)
	if err != nil {
		// Log a given failure once, not on every scan.
		if msg := err.Error(); msg != st.lastErr {
			st.lastErr = msg
			d.Log.WarnContext(ctx, "cannot scan watched folder", "watch", watch, "error", err)
		}
		return
	}
	st.lastErr = ""
	if !st.baselined {
		st.baselined = true
		st.known = cur
		return
	}
	s.deletes(ctx, d, watch, st, cur)
	s.changes(ctx, d, watch, st, cur)
}

func (s *Source) deletes(ctx context.Context, d app.Deps, watch string, st *watchState, cur map[string]Entry) {
	for _, p := range sortedPaths(st.known) {
		if _, ok := cur[p]; ok {
			continue
		}
		data := map[string]any{"path": p, "watch": watch}
		if s.publish(ctx, d, "agentsone.files.deleted", p, data) {
			delete(st.known, p)
		}
	}
	for p := range st.pending {
		if _, ok := cur[p]; !ok {
			delete(st.pending, p)
		}
	}
}

func (s *Source) changes(ctx context.Context, d app.Deps, watch string, st *watchState, cur map[string]Entry) {
	for _, p := range sortedPaths(cur) {
		now := cur[p]
		old, told := st.known[p]
		if told && old == now {
			delete(st.pending, p)
			continue
		}
		if st.pending[p] != now {
			st.pending[p] = now // new or still changing: wait for the next scan
			continue
		}
		eventType := "agentsone.files.created"
		if told {
			eventType = "agentsone.files.modified"
		}
		data := map[string]any{"path": p, "watch": watch, "size_bytes": now.Size}
		if s.publish(ctx, d, eventType, p, data) {
			st.known[p] = now
			delete(st.pending, p)
		}
	}
}

// publish reports whether the event went out; a failed one is tried again on
// the next scan.
func (s *Source) publish(ctx context.Context, d app.Deps, eventType, subject string, data map[string]any) bool {
	draft, err := domain.NewDraft(eventType, subject, data)
	if err != nil {
		d.Log.ErrorContext(ctx, "bad event draft", "type", eventType, "error", err)
		return true // it will never be valid; do not retry for ever
	}
	if err := d.Publisher.Publish(ctx, draft); err != nil {
		if ctx.Err() == nil {
			d.Log.WarnContext(ctx, "files event not published", "type", eventType, "error", err)
		}
		return false
	}
	return true
}

func sortedPaths(m map[string]Entry) []string {
	out := make([]string, 0, len(m))
	for p := range m {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// Disk is the real Walker over the machine's file system.
type Disk struct{}

var _ Walker = Disk{}

// Walk lists regular files below watch. Links, devices and other special
// files are left out, and links are not followed.
func (Disk) Walk(watch string, limit int) (map[string]Entry, error) {
	out := map[string]Entry{}
	err := filepath.WalkDir(watch, func(path string, de fs.DirEntry, err error) error {
		if err != nil {
			if path == watch {
				return err
			}
			return nil // a file that vanished or cannot be read mid-scan is skipped
		}
		if !de.Type().IsRegular() {
			return nil
		}
		info, err := de.Info()
		if err != nil {
			return nil //nolint:nilerr // vanished between listing and stat
		}
		if len(out) >= limit {
			return ErrTooManyFiles
		}
		out[path] = Entry{Size: info.Size(), ModTime: info.ModTime()}
		return nil
	})
	if err != nil {
		return nil, err //nolint:wrapcheck // the walk error already names the path
	}
	return out, nil
}
