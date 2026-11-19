// SPDX-License-Identifier: MIT

package app_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/bus/domain"
	"github.com/SDK-E/agentsone/internal/core"
)

func TestStream_LiveMessagesOnly_WithoutAResumePoint(t *testing.T) {
	f := newFixture(t)
	seed(t, f)
	ctx, cancel := context.WithCancel(asHuman("ada"))
	defer cancel()
	ch, err := f.svc.Stream(ctx, "", nil)
	if err != nil {
		t.Fatal(err)
	}

	live := post(asAgent("builder"), t, f, toChannel("general", "live"))

	if got := recv(t, ch); got.ID != live.ID {
		t.Fatalf("first streamed message = %s, want the live one %s", got.ID, live.ID)
	}
}

func TestStream_ReplaysAfterTheResumePointThenGoesLive(t *testing.T) {
	f := newFixture(t)
	msgs := seed(t, f)
	ctx, cancel := context.WithCancel(asAgent("outsider"))
	defer cancel()
	after := msgs[0].Sequence
	ch, err := f.svc.Stream(ctx, "", &after)
	if err != nil {
		t.Fatal(err)
	}

	// d1 and t1 are hidden from the outsider, so d2 comes first.
	if got := recv(t, ch); got.Text != "d2" {
		t.Fatalf("replayed message = %q, want d2", got.Text)
	}
	live := post(asAgent("builder"), t, f, toChannel("general", "live"))
	if got := recv(t, ch); got.ID != live.ID {
		t.Fatalf("live message = %s, want %s", got.ID, live.ID)
	}
}

func TestStream_NeverShowsWhatTheCallerMayNotSee(t *testing.T) {
	f := newFixture(t)
	seed(t, f)
	ctx, cancel := context.WithCancel(asAgent("outsider"))
	defer cancel()
	ch, err := f.svc.Stream(ctx, "", nil)
	if err != nil {
		t.Fatal(err)
	}

	post(asAgent("builder"), t, f, toAgent("intern", "private"))
	post(asAgent("builder"), t, f, toChannel("team.founder", "team only"))
	visible := post(asAgent("builder"), t, f, toChannel("general", "open"))

	if got := recv(t, ch); got.ID != visible.ID {
		t.Fatalf("streamed %q before the visible message", got.Text)
	}
}

func TestStream_ChannelFilter(t *testing.T) {
	f := newFixture(t)
	seed(t, f)
	mustChannel(asAgent("builder"), t, f, channel("ops"))
	ctx, cancel := context.WithCancel(asHuman("ada"))
	defer cancel()
	ch, err := f.svc.Stream(ctx, "ops", nil)
	if err != nil {
		t.Fatal(err)
	}

	post(asAgent("builder"), t, f, toChannel("general", "elsewhere"))
	post(asAgent("builder"), t, f, toAgent("intern", "direct"))
	want := post(asAgent("builder"), t, f, toChannel("ops", "here"))

	if got := recv(t, ch); got.ID != want.ID {
		t.Fatalf("streamed %q, want the ops message", got.Text)
	}
}

func TestStream_RefusesABadChannelName(t *testing.T) {
	f := newFixture(t)

	if _, err := f.svc.Stream(asHuman("ada"), "Bad Name", nil); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("Stream() error = %v, want ErrInvalid", err)
	}
}

func TestStream_ClosesWhenTheContextEnds(t *testing.T) {
	f := newFixture(t)
	ctx, cancel := context.WithCancel(asHuman("ada"))
	ch, err := f.svc.Stream(ctx, "", nil)
	if err != nil {
		t.Fatal(err)
	}

	cancel()

	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("got a message after cancel")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stream stayed open after cancel")
	}
}

func TestStream_ASlowSubscriberIsClosedAndCanResumeWithoutLoss(t *testing.T) {
	f := newFixture(t)
	mustChannel(asHuman("ada"), t, f, channel("general"))
	ctx, cancel := context.WithCancel(asHuman("ada"))
	defer cancel()
	ch, err := f.svc.Stream(ctx, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	const total = 600
	for i := range total {
		if i%100 == 0 {
			f.clock.Advance(window) // stay under the send rate
		}
		post(asAgent("builder"), t, f, toChannel("general", "x"))
	}

	var lastSeen uint64
	received := 0
	for m := range ch {
		lastSeen = m.Sequence
		received++
	}

	if received >= total {
		t.Fatalf("received %d of %d: the slow subscriber was never closed", received, total)
	}
	resumed, err := f.svc.Stream(ctx, "", &lastSeen)
	if err != nil {
		t.Fatal(err)
	}
	for range total - received {
		recv(t, resumed)
	}
}

// TESTING A7: many senders and readers at once lose no message, repeat none
// and keep the order. Run under -race.
func TestStream_ConcurrentSendersAreDeliveredOnceAndInOrder(t *testing.T) {
	f := newFixture(t)
	mustChannel(asHuman("ada"), t, f, channel("general"))
	ctx, cancel := context.WithCancel(asHuman("ada"))
	defer cancel()
	const senders, each = 4, 25
	streams := make([]<-chan domain.Message, 3)
	for i := range streams {
		ch, err := f.svc.Stream(ctx, "general", nil)
		if err != nil {
			t.Fatal(err)
		}
		streams[i] = ch
	}

	var wg sync.WaitGroup
	for s := range senders {
		wg.Add(1)
		go func() {
			defer wg.Done()
			name := []string{"builder", "intern", "outsider", "founder"}[s]
			for range each {
				if _, err := f.svc.Post(asAgent(name), toChannel("general", "x")); err != nil {
					t.Errorf("Post() error = %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()

	for i, ch := range streams {
		var last uint64
		seen := map[string]bool{}
		for range senders * each {
			m := recv(t, ch)
			if m.Sequence <= last || seen[m.ID] {
				t.Fatalf("stream %d: sequence %d after %d, or a repeat", i, m.Sequence, last)
			}
			last, seen[m.ID] = m.Sequence, true
		}
	}
}
