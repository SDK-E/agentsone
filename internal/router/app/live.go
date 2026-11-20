// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"encoding/base64"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/router/domain"
)

// LiveList keeps every agent's latest one-line and shows it next to the
// agent's profile. It is the daemon's core.LiveList port.
type LiveList struct {
	lines    domain.LineRepository
	profiles Profiles
	clock    core.Clock
	log      *slog.Logger
	hub      *hub
}

var _ core.LiveList = (*LiveList)(nil)

// NewLiveList returns a live list over repo. A nil log uses slog.Default.
func NewLiveList(repo domain.LineRepository, profiles Profiles, clock core.Clock, log *slog.Logger) *LiveList {
	if log == nil {
		log = slog.Default()
	}
	return &LiveList{lines: repo, profiles: profiles, clock: clock, log: log, hub: newHub()}
}

// UpdateLine sets the agent's line from runner output. The text is cleaned;
// an empty or over-long line is core.ErrInvalid.
func (l *LiveList) UpdateLine(ctx context.Context, agent, text string) error {
	line, err := domain.NewLine(text)
	if err != nil {
		return fmt.Errorf("update line of %s: %w", agent, err)
	}
	stored := domain.StoredLine{Agent: agent, Line: line, Updated: l.clock.Now()}
	if err := l.lines.Put(ctx, stored); err != nil {
		return fmt.Errorf("store line of %s: %w", agent, err)
	}
	profiles, err := l.profiles.Profiles(ctx)
	if err != nil {
		// The line is stored and watchers pick it up on their next
		// reconcile, so the caller's work is done; only the push is lost.
		l.log.WarnContext(ctx, "live line stored but not pushed: agents unreadable", "agent", agent, "error", err)
		return nil
	}
	for _, p := range profiles {
		if p.Agent == agent {
			l.hub.publish(Change{Entry: &domain.Entry{Profile: p, Line: &line, Updated: stored.Updated}})
		}
	}
	return nil
}

// Lines returns every stored line, sorted by agent.
func (l *LiveList) Lines(ctx context.Context) ([]core.LiveLine, error) {
	stored, err := l.lines.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list lines: %w", err)
	}
	out := make([]core.LiveLine, len(stored))
	for i, s := range stored {
		out[i] = core.LiveLine{Agent: s.Agent, Line: s.Line.String(), Updated: s.Updated}
	}
	return out, nil
}

// Forget drops a retired agent's line and tells watchers it left.
func (l *LiveList) Forget(ctx context.Context, agent string) error {
	if err := l.lines.Delete(ctx, agent); err != nil {
		return fmt.Errorf("forget line of %s: %w", agent, err)
	}
	l.hub.publish(Change{Removed: agent})
	return nil
}

// Entries returns the live list: every agent of the society, most recent
// line first; agents without a line follow by name.
func (l *LiveList) Entries(ctx context.Context) ([]domain.Entry, error) {
	profiles, err := l.profiles.Profiles(ctx)
	if err != nil {
		return nil, fmt.Errorf("list agents: %w", err)
	}
	stored, err := l.lines.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list lines: %w", err)
	}
	byAgent := make(map[string]domain.StoredLine, len(stored))
	for _, s := range stored {
		byAgent[s.Agent] = s
	}
	entries := make([]domain.Entry, 0, len(profiles))
	for _, p := range profiles {
		e := domain.Entry{Profile: p}
		if s, ok := byAgent[p.Agent]; ok {
			line := s.Line
			e.Line, e.Updated = &line, s.Updated
		}
		entries = append(entries, e)
	}
	sort.SliceStable(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		if (a.Line == nil) != (b.Line == nil) {
			return a.Line != nil
		}
		if !a.Updated.Equal(b.Updated) {
			return a.Updated.After(b.Updated)
		}
		return a.Agent < b.Agent
	})
	return entries, nil
}

// Page returns up to limit entries after cursor, and the cursor of the next
// page, or "" on the last page. A cursor that is not one of ours is
// core.ErrInvalid.
func (l *LiveList) Page(ctx context.Context, cursor string, limit int) ([]domain.Entry, string, error) {
	offset, err := decodeCursor(cursor)
	if err != nil {
		return nil, "", err
	}
	entries, err := l.Entries(ctx)
	if err != nil {
		return nil, "", err
	}
	if offset >= len(entries) {
		return []domain.Entry{}, "", nil
	}
	end := min(offset+limit, len(entries))
	next := ""
	if end < len(entries) {
		next = encodeCursor(end)
	}
	return entries[offset:end], next, nil
}

func encodeCursor(offset int) string {
	return base64.RawURLEncoding.EncodeToString([]byte("off=" + strconv.Itoa(offset)))
}

func decodeCursor(c string) (int, error) {
	if c == "" {
		return 0, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(c)
	rest, ok := strings.CutPrefix(string(raw), "off=")
	if err != nil || !ok {
		return 0, fmt.Errorf("cursor %q is not a live list cursor: %w", c, core.ErrInvalid)
	}
	n, err := strconv.Atoi(rest)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("cursor %q is not a live list cursor: %w", c, core.ErrInvalid)
	}
	return n, nil
}

// Watch streams changes until ctx ends. It first sends the whole live list,
// so a client that reconnects always catches up. Lines arrive as they are
// set; on every tick the list is read again to catch state changes, new
// agents and retired ones.
func (l *LiveList) Watch(ctx context.Context, ticks <-chan time.Time) <-chan Change {
	out := make(chan Change, hubBuffer)
	live := l.hub.subscribe(ctx)
	go func() {
		defer close(out)
		w := watcher{known: map[string]domain.Entry{}, out: out, ctx: ctx}
		w.reconcile(l.Entries(ctx))
		for {
			select {
			case <-ctx.Done():
				return
			case c, ok := <-live:
				if !ok {
					return
				}
				w.apply(c)
			case <-ticks:
				w.reconcile(l.Entries(ctx))
			}
		}
	}()
	return out
}

// watcher remembers what a client has been sent, so it sends only changes.
type watcher struct {
	ctx   context.Context //nolint:containedctx // lives as long as the stream it serves
	known map[string]domain.Entry
	out   chan<- Change
}

func (w *watcher) apply(c Change) {
	if c.Entry == nil {
		if _, ok := w.known[c.Removed]; ok {
			delete(w.known, c.Removed)
			w.send(c)
		}
		return
	}
	w.update(*c.Entry)
}

func (w *watcher) update(e domain.Entry) {
	if old, ok := w.known[e.Agent]; ok && sameEntry(old, e) {
		return
	}
	w.known[e.Agent] = e
	w.send(Change{Entry: &e})
}

func (w *watcher) reconcile(entries []domain.Entry, err error) {
	if err != nil {
		return // keep what the client has; the next tick tries again
	}
	present := make(map[string]bool, len(entries))
	for _, e := range entries {
		present[e.Agent] = true
		w.update(e)
	}
	for agent := range w.known {
		if !present[agent] {
			delete(w.known, agent)
			w.send(Change{Removed: agent})
		}
	}
}

func (w *watcher) send(c Change) {
	select {
	case w.out <- c:
	case <-w.ctx.Done():
	}
}

func sameEntry(a, b domain.Entry) bool {
	return a.Profile == b.Profile && a.OneLine() == b.OneLine() && a.Updated.Equal(b.Updated)
}
