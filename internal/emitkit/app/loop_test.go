// SPDX-License-Identifier: MIT

package app_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/emitkit/app"
	"github.com/SDK-E/agentsone/internal/emitkit/domain"
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

func draft(t *testing.T) domain.Draft {
	t.Helper()
	d, err := domain.NewDraft("agentsone.clock.tick", "", map[string]string{"minute": "x"})
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestRetrying_RetriesTransientFailuresWithBackoff(t *testing.T) {
	t.Parallel()
	inner := &emitkittest.Publisher{Fail: func(n int) error {
		if n < 3 {
			return errors.New("connection refused")
		}
		return nil
	}}
	sl := &emitkittest.Sleeper{Clock: emitkittest.NewClock(t0)}
	p := app.Retrying(inner, sl, emitkittest.Logger(), 4, time.Second)

	if err := p.Publish(context.Background(), draft(t)); err != nil {
		t.Fatalf("Publish error = %v", err)
	}
	if inner.Calls() != 3 {
		t.Fatalf("calls = %d, want 3", inner.Calls())
	}
	if got := sl.Slept(); len(got) != 2 || got[0] != time.Second || got[1] != 2*time.Second {
		t.Fatalf("backoff = %v, want [1s 2s]", got)
	}
}

func TestRetrying_GivesUpAfterAttempts(t *testing.T) {
	t.Parallel()
	boom := errors.New("daemon down")
	inner := &emitkittest.Publisher{Fail: func(int) error { return boom }}
	p := app.Retrying(inner, &emitkittest.Sleeper{}, emitkittest.Logger(), 3, time.Second)

	err := p.Publish(context.Background(), draft(t))

	if !errors.Is(err, boom) {
		t.Fatalf("error = %v, want the last cause", err)
	}
	if inner.Calls() != 3 {
		t.Fatalf("calls = %d, want 3", inner.Calls())
	}
}

func TestRetrying_DoesNotRetryARejection(t *testing.T) {
	t.Parallel()
	inner := &emitkittest.Publisher{Fail: func(int) error { return app.ErrRejected }}
	p := app.Retrying(inner, &emitkittest.Sleeper{}, emitkittest.Logger(), 5, time.Second)

	err := p.Publish(context.Background(), draft(t))

	if !errors.Is(err, app.ErrRejected) || inner.Calls() != 1 {
		t.Fatalf("error = %v after %d calls, want ErrRejected after 1", err, inner.Calls())
	}
}

func TestRetrying_StopsWhenCancelledDuringBackoff(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	inner := &emitkittest.Publisher{Fail: func(int) error { return errors.New("down") }}
	sl := &emitkittest.Sleeper{Cancel: cancel, Limit: 1}
	p := app.Retrying(inner, sl, emitkittest.Logger(), 5, time.Second)

	err := p.Publish(ctx, draft(t))

	if !errors.Is(err, context.Canceled) || inner.Calls() != 1 {
		t.Fatalf("error = %v after %d calls, want context.Canceled after 1", err, inner.Calls())
	}
}

func TestRetrying_AtLeastOneAttempt(t *testing.T) {
	t.Parallel()
	inner := &emitkittest.Publisher{}
	p := app.Retrying(inner, &emitkittest.Sleeper{}, emitkittest.Logger(), 0, time.Second)
	if err := p.Publish(context.Background(), draft(t)); err != nil || inner.Calls() != 1 {
		t.Fatalf("Publish = %v after %d calls, want nil after 1", err, inner.Calls())
	}
}
