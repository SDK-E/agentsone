// SPDX-License-Identifier: MIT

package clock_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/emitters/clock"
	"github.com/SDK-E/agentsone/internal/emitkit/domain"
	"github.com/SDK-E/agentsone/internal/emitkit/emitkittest"
)

func run(t *testing.T, start time.Time, ticks int, pub *emitkittest.Publisher, onSleep func(time.Duration, *emitkittest.Clock)) []domain.Draft {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	clk := emitkittest.NewClock(start)
	sl := &emitkittest.Sleeper{Clock: clk, Cancel: cancel, Limit: ticks + 1}
	if onSleep != nil {
		sl.OnSleep = func(d time.Duration) { onSleep(d, clk) }
	}
	if err := clock.New().Run(ctx, emitkittest.Deps(clk, sl, pub)); err != nil {
		t.Fatalf("Run error = %v, want nil on cancel", err)
	}
	return pub.Drafts()
}

func minutes(t *testing.T, drafts []domain.Draft) []string {
	t.Helper()
	var out []string
	for _, d := range drafts {
		if d.Type != "agentsone.clock.tick" {
			t.Fatalf("type = %s, want agentsone.clock.tick", d.Type)
		}
		out = append(out, string(d.Data))
	}
	return out
}

func TestRun_TicksAtTheStartOfEachMinute(t *testing.T) {
	t.Parallel()
	pub := &emitkittest.Publisher{}
	start := time.Date(2026, 11, 20, 0, 0, 20, 0, time.UTC)

	drafts := run(t, start, 3, pub, nil)

	want := []string{
		`{"minute":"2026-11-20T00:01:00Z"}`,
		`{"minute":"2026-11-20T00:02:00Z"}`,
		`{"minute":"2026-11-20T00:03:00Z"}`,
	}
	got := minutes(t, drafts)
	if len(got) != len(want) {
		t.Fatalf("ticks = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("tick %d = %s, want %s", i, got[i], want[i])
		}
	}
	emitkittest.RequireInCatalog(t, "clock", drafts)
}

func TestRun_FirstSleepEndsOnTheMinute(t *testing.T) {
	t.Parallel()
	var slept []time.Duration
	run(t, time.Date(2026, 11, 20, 0, 0, 20, 500, time.UTC), 1, &emitkittest.Publisher{}, func(d time.Duration, _ *emitkittest.Clock) {
		slept = append(slept, d)
	})
	if want := 40*time.Second - 500*time.Nanosecond; slept[0] != want {
		t.Fatalf("first sleep = %v, want %v", slept[0], want)
	}
}

func TestRun_StartingOnTheMinuteWaitsForTheNextOne(t *testing.T) {
	t.Parallel()
	drafts := run(t, time.Date(2026, 11, 20, 0, 5, 0, 0, time.UTC), 1, &emitkittest.Publisher{}, nil)
	if got := minutes(t, drafts); len(got) != 1 || got[0] != `{"minute":"2026-11-20T00:06:00Z"}` {
		t.Fatalf("ticks = %v, want only 00:06", got)
	}
}

func TestRun_AfterASuspendItSkipsTheMissedMinutes(t *testing.T) {
	t.Parallel()
	pub := &emitkittest.Publisher{}
	first := true
	drafts := run(t, time.Date(2026, 11, 20, 0, 0, 30, 0, time.UTC), 2, pub, func(_ time.Duration, c *emitkittest.Clock) {
		if first { // the machine slept for ten minutes during the first wait
			first = false
			c.Advance(10 * time.Minute)
		}
	})
	got := minutes(t, drafts)
	if len(got) != 2 || got[0] != `{"minute":"2026-11-20T00:01:00Z"}` || got[1] != `{"minute":"2026-11-20T00:12:00Z"}` {
		t.Fatalf("ticks = %v, want 00:01 then 00:12 and nothing between", got)
	}
}

func TestRun_AFailedPublishDoesNotStopTheClock(t *testing.T) {
	t.Parallel()
	pub := &emitkittest.Publisher{Fail: func(n int) error {
		if n == 1 {
			return errors.New("daemon restarting")
		}
		return nil
	}}
	drafts := run(t, time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC), 2, pub, nil)
	if got := minutes(t, drafts); len(got) != 1 || got[0] != `{"minute":"2026-11-20T00:02:00Z"}` {
		t.Fatalf("ticks = %v, want the second minute only", got)
	}
}
