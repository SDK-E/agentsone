// SPDX-License-Identifier: MIT

package app_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/core/coretest"
	"github.com/SDK-E/agentsone/internal/router/adapters/memory"
	"github.com/SDK-E/agentsone/internal/router/app"
	"github.com/SDK-E/agentsone/internal/router/routertest"
)

func newLive(t *testing.T) (*app.LiveList, *routertest.Profiles, *coretest.Clock) {
	t.Helper()
	clock := coretest.NewClock(coretest.Epoch)
	profiles := routertest.NewProfiles(routertest.Agent("amy", "Writer"), routertest.Agent("bob", "Builder"), routertest.Agent("cat", "Keeper"))
	return app.NewLiveList(memory.NewLines(), profiles, clock), profiles, clock
}

func TestUpdateLine_CleansAndStoresTheLine(t *testing.T) {
	t.Parallel()
	live, _, clock := newLive(t)

	if err := live.UpdateLine(context.Background(), "amy", "Goal: write.\nNow:   drafting."); err != nil {
		t.Fatal(err)
	}

	lines, err := live.Lines(context.Background())
	if err != nil || len(lines) != 1 {
		t.Fatalf("lines = %+v, %v", lines, err)
	}
	if lines[0].Line != "Goal: write. Now: drafting." || !lines[0].Updated.Equal(clock.Now()) {
		t.Fatalf("line = %+v", lines[0])
	}
}

func TestUpdateLine_RejectsBadLines(t *testing.T) {
	t.Parallel()
	live, _, _ := newLive(t)
	for _, in := range []string{"", "  \n", strings.Repeat("x", core.MaxLiveLine+1)} {
		if err := live.UpdateLine(context.Background(), "amy", in); !errors.Is(err, core.ErrInvalid) {
			t.Errorf("UpdateLine(%d bytes) error = %v", len(in), err)
		}
	}
}

func TestEntries_ListsEveryAgentMostRecentFirst(t *testing.T) {
	t.Parallel()
	live, _, clock := newLive(t)
	ctx := context.Background()
	_ = live.UpdateLine(ctx, "bob", "first")
	clock.Advance(time.Second)
	_ = live.UpdateLine(ctx, "amy", "second")

	entries, err := live.Entries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := []string{entries[0].Agent, entries[1].Agent, entries[2].Agent}
	if got[0] != "amy" || got[1] != "bob" || got[2] != "cat" || entries[2].Line != nil {
		t.Fatalf("order = %v", got)
	}
}

func TestEntries_IgnoresLinesOfAgentsThatAreGone(t *testing.T) {
	t.Parallel()
	live, profiles, _ := newLive(t)
	_ = live.UpdateLine(context.Background(), "amy", "hello")
	profiles.Set(routertest.Agent("bob", "Builder"))

	entries, _ := live.Entries(context.Background())
	if len(entries) != 1 || entries[0].Agent != "bob" {
		t.Fatalf("entries = %+v", entries)
	}
}

func TestEntries_FailWhenAgentsCannotBeRead(t *testing.T) {
	t.Parallel()
	live, profiles, _ := newLive(t)
	profiles.Err = errors.New("db down")
	if _, err := live.Entries(context.Background()); err == nil {
		t.Fatal("no error")
	}
	if _, _, err := live.Page(context.Background(), "", 10); err == nil {
		t.Fatal("no error from Page")
	}
}

func TestPage_WalksTheListByCursor(t *testing.T) {
	t.Parallel()
	live, _, _ := newLive(t)
	ctx := context.Background()

	first, next, err := live.Page(ctx, "", 2)
	if err != nil || len(first) != 2 || next == "" {
		t.Fatalf("page 1 = %d entries, next %q, %v", len(first), next, err)
	}
	second, next, err := live.Page(ctx, next, 2)
	if err != nil || len(second) != 1 || next != "" {
		t.Fatalf("page 2 = %d entries, next %q, %v", len(second), next, err)
	}
	if first[0].Agent == second[0].Agent {
		t.Fatal("pages overlap")
	}
	beyond, next, err := live.Page(ctx, "b2ZmPTk5", 2)
	if err != nil || len(beyond) != 0 || next != "" {
		t.Fatalf("beyond = %d, %q, %v", len(beyond), next, err)
	}
}

func TestPage_RejectsForeignCursors(t *testing.T) {
	t.Parallel()
	live, _, _ := newLive(t)
	for _, c := range []string{"!!!", "c2VxPTUw", "b2ZmPS0x", "b2ZmPXg"} {
		if _, _, err := live.Page(context.Background(), c, 5); !errors.Is(err, core.ErrInvalid) {
			t.Errorf("cursor %q error = %v", c, err)
		}
	}
}

func recv(t *testing.T, ch <-chan app.Change) app.Change {
	t.Helper()
	select {
	case c, ok := <-ch:
		if !ok {
			t.Fatal("watch ended")
		}
		return c
	case <-time.After(5 * time.Second):
		t.Fatal("no change within 5s")
		return app.Change{}
	}
}

func TestWatch_SendsTheListThenLiveChanges(t *testing.T) {
	t.Parallel()
	live, _, _ := newLive(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ticks := make(chan time.Time)
	ch := live.Watch(ctx, ticks)

	seen := map[string]bool{}
	for range 3 {
		c := recv(t, ch)
		seen[c.Entry.Agent] = true
	}
	if !seen["amy"] || !seen["bob"] || !seen["cat"] {
		t.Fatalf("snapshot = %v", seen)
	}

	if err := live.UpdateLine(ctx, "bob", "Goal: build. Now: building."); err != nil {
		t.Fatal(err)
	}
	c := recv(t, ch)
	if c.Entry == nil || c.Entry.Agent != "bob" || c.Entry.OneLine() != "Goal: build. Now: building." {
		t.Fatalf("change = %+v", c)
	}
}

func TestWatch_ReconcileFindsStateChangesNewAndRetiredAgents(t *testing.T) {
	t.Parallel()
	live, profiles, _ := newLive(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ticks := make(chan time.Time)
	ch := live.Watch(ctx, ticks)
	for range 3 {
		recv(t, ch)
	}

	working := routertest.Agent("amy", "Writer")
	working.State = "working"
	profiles.Set(working, routertest.Agent("bob", "Builder"), routertest.Agent("dan", "New"))
	ticks <- time.Time{}

	got := map[string]app.Change{}
	for range 3 {
		c := recv(t, ch)
		if c.Entry != nil {
			got[c.Entry.Agent] = c
		} else {
			got["removed:"+c.Removed] = c
		}
	}
	if got["amy"].Entry == nil || got["amy"].Entry.State != "working" {
		t.Fatalf("amy = %+v", got["amy"])
	}
	if got["dan"].Entry == nil {
		t.Fatalf("dan = %+v", got["dan"])
	}
	if _, ok := got["removed:cat"]; !ok {
		t.Fatalf("cat was not reported as removed: %+v", got)
	}
}

func TestWatch_SendsNothingForAnUnchangedList(t *testing.T) {
	t.Parallel()
	live, _, _ := newLive(t)
	ctx, cancel := context.WithCancel(context.Background())
	ticks := make(chan time.Time)
	ch := live.Watch(ctx, ticks)
	for range 3 {
		recv(t, ch)
	}

	ticks <- time.Time{}
	ticks <- time.Time{} // the second send returns only after the first tick was handled
	cancel()
	for c := range ch {
		t.Fatalf("unexpected change %+v", c)
	}
}

func TestForget_TellsWatchersTheAgentLeft(t *testing.T) {
	t.Parallel()
	live, _, _ := newLive(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := live.Watch(ctx, make(chan time.Time))
	for range 3 {
		recv(t, ch)
	}

	if err := live.Forget(ctx, "cat"); err != nil {
		t.Fatal(err)
	}
	if c := recv(t, ch); c.Removed != "cat" {
		t.Fatalf("change = %+v", c)
	}
}

func TestWatch_EndsWhenTheContextEnds(t *testing.T) {
	t.Parallel()
	live, _, _ := newLive(t)
	ctx, cancel := context.WithCancel(context.Background())
	ch := live.Watch(ctx, make(chan time.Time))
	for range 3 {
		recv(t, ch)
	}
	cancel()
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("change after cancel")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("watch did not end")
	}
}
