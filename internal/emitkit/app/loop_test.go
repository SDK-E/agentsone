// SPDX-License-Identifier: MIT

package app_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/emitkit/app"
	"github.com/SDK-E/agentsone/internal/emitkit/emitkittest"
)

var t0 = time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)

func TestEvery_StepsNowThenOnEachInterval(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sl := &emitkittest.Sleeper{Clock: emitkittest.NewClock(t0), Cancel: cancel, Limit: 2}
	steps := 0

	err := app.Every(ctx, sl, time.Minute, emitkittest.Logger(), func(context.Context) error {
		steps++
		return nil
	})

	if err != nil {
		t.Fatalf("Every error = %v, want nil on cancel", err)
	}
	if steps != 2 {
		t.Fatalf("steps = %d, want 2 (one now, one after the first sleep)", steps)
	}
	if got := sl.Slept(); len(got) != 2 || got[0] != time.Minute {
		t.Fatalf("slept = %v, want two sleeps of a minute", got)
	}
}

func TestEvery_StepErrorsAreLoggedNotFatal(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sl := &emitkittest.Sleeper{Clock: emitkittest.NewClock(t0), Cancel: cancel, Limit: 2}
	steps := 0

	err := app.Every(ctx, sl, time.Second, emitkittest.Logger(), func(context.Context) error {
		steps++
		return errors.New("sample failed")
	})

	if err != nil || steps != 2 {
		t.Fatalf("Every = (%v), steps = %d; want nil error and 2 steps", err, steps)
	}
}

func TestEvery_StopsBeforeFirstStepWhenAlreadyCancelled(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	steps := 0
	err := app.Every(ctx, &emitkittest.Sleeper{}, time.Second, emitkittest.Logger(), func(context.Context) error {
		steps++
		return nil
	})
	if err != nil || steps != 0 {
		t.Fatalf("Every = %v with %d steps, want nil and 0", err, steps)
	}
}

func TestTimer_ReturnsWhenCancelled(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := (app.Timer{}).Sleep(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Fatalf("Sleep error = %v, want context.Canceled", err)
	}
}

func TestTimer_WaitsForTheDuration(t *testing.T) {
	t.Parallel()
	if err := (app.Timer{}).Sleep(context.Background(), time.Millisecond); err != nil {
		t.Fatalf("Sleep error = %v", err)
	}
}
