// SPDX-License-Identifier: MIT

package domain_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/edge/control/domain"
)

func failTimes(l *domain.Limiter, n int, now time.Time) {
	for range n {
		l.Fail(now)
	}
}

func TestLimiter_AllowsTheFreeFailuresWithoutWaiting(t *testing.T) {
	t.Parallel()
	l := domain.NewLimiter(5, time.Second, 15*time.Minute)

	failTimes(l, 5, t0)

	if wait := l.Wait(t0); wait != 0 {
		t.Fatalf("Wait() after five failures = %v, want 0", wait)
	}
}

func TestLimiter_LocksAfterTheFreeFailuresWithADoublingWait(t *testing.T) {
	t.Parallel()
	tests := []struct {
		failures int
		want     time.Duration
	}{
		{6, time.Second},
		{7, 2 * time.Second},
		{8, 4 * time.Second},
		{11, 32 * time.Second},
		{12, time.Minute},
		{80, time.Minute},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("%d failures", tt.failures), func(t *testing.T) {
			t.Parallel()
			l := domain.NewLimiter(5, time.Second, time.Minute)

			failTimes(l, tt.failures, t0)

			if got := l.Wait(t0); got != tt.want {
				t.Fatalf("Wait() after %d failures = %v, want %v", tt.failures, got, tt.want)
			}
		})
	}
}

func TestLimiter_AllowsAgainOnceTheWaitIsOver(t *testing.T) {
	t.Parallel()
	l := domain.NewLimiter(5, time.Second, 15*time.Minute)
	failTimes(l, 7, t0)

	if got := l.Wait(t0.Add(time.Second)); got != time.Second {
		t.Fatalf("Wait() half way = %v, want 1s", got)
	}
	if got := l.Wait(t0.Add(2 * time.Second)); got != 0 {
		t.Fatalf("Wait() at the end of the lock = %v, want 0", got)
	}
}

func TestLimiter_SucceedClearsTheFailures(t *testing.T) {
	t.Parallel()
	l := domain.NewLimiter(5, time.Second, 15*time.Minute)
	failTimes(l, 9, t0)

	l.Succeed()
	failTimes(l, 5, t0)

	if got := l.Wait(t0); got != 0 {
		t.Fatalf("Wait() after a success and five failures = %v, want 0", got)
	}
}
