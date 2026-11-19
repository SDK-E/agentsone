// SPDX-License-Identifier: MIT

package domain_test

import (
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/core/coretest"
	"github.com/SDK-E/agentsone/internal/events/domain"
)

var twoPerMinute = domain.WakeRate{Max: 2, Window: time.Minute}

func TestWakeLimiter_AllowsFiringsUpToTheLimit(t *testing.T) {
	t.Parallel()
	limiter := domain.NewWakeLimiter(coretest.NewClock(coretest.Epoch))

	for i := 1; i <= 2; i++ {
		if !limiter.Allow("trg_a", twoPerMinute) {
			t.Fatalf("firing %d of 2 was refused", i)
		}
	}
}

func TestWakeLimiter_RefusesOneOverTheLimit(t *testing.T) {
	t.Parallel()
	limiter := domain.NewWakeLimiter(coretest.NewClock(coretest.Epoch))
	limiter.Allow("trg_a", twoPerMinute)
	limiter.Allow("trg_a", twoPerMinute)

	if limiter.Allow("trg_a", twoPerMinute) {
		t.Fatal("third firing inside the window was allowed")
	}
}

func TestWakeLimiter_AllowsAgainAfterTheWindow(t *testing.T) {
	t.Parallel()
	clock := coretest.NewClock(coretest.Epoch)
	limiter := domain.NewWakeLimiter(clock)
	limiter.Allow("trg_a", twoPerMinute)
	limiter.Allow("trg_a", twoPerMinute)

	clock.Advance(time.Minute + time.Second)

	if !limiter.Allow("trg_a", twoPerMinute) {
		t.Fatal("firing after the window was refused")
	}
}

func TestWakeLimiter_WindowEdge(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		after time.Duration
		want  bool
	}{
		{"one nanosecond before the window ends", time.Minute - time.Nanosecond, false},
		{"exactly when the window ends", time.Minute, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			clock := coretest.NewClock(coretest.Epoch)
			limiter := domain.NewWakeLimiter(clock)
			oneAMinute := domain.WakeRate{Max: 1, Window: time.Minute}
			limiter.Allow("trg_a", oneAMinute)

			clock.Advance(tt.after)

			if got := limiter.Allow("trg_a", oneAMinute); got != tt.want {
				t.Fatalf("Allow() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestWakeLimiter_RefusedFiringsDoNotExtendTheWindow(t *testing.T) {
	t.Parallel()
	clock := coretest.NewClock(coretest.Epoch)
	limiter := domain.NewWakeLimiter(clock)
	oneAMinute := domain.WakeRate{Max: 1, Window: time.Minute}
	limiter.Allow("trg_a", oneAMinute)
	clock.Advance(30 * time.Second)
	limiter.Allow("trg_a", oneAMinute) // refused

	clock.Advance(30 * time.Second)

	if !limiter.Allow("trg_a", oneAMinute) {
		t.Fatal("a refused firing pushed the window out")
	}
}

func TestWakeLimiter_KeepsOneWindowPerKey(t *testing.T) {
	t.Parallel()
	limiter := domain.NewWakeLimiter(coretest.NewClock(coretest.Epoch))
	oneAMinute := domain.WakeRate{Max: 1, Window: time.Minute}
	limiter.Allow("trg_a", oneAMinute)

	if !limiter.Allow("trg_b", oneAMinute) {
		t.Fatal("trigger b was limited by trigger a")
	}
}

func TestWakeLimiter_ForgetStartsANewWindow(t *testing.T) {
	t.Parallel()
	limiter := domain.NewWakeLimiter(coretest.NewClock(coretest.Epoch))
	oneAMinute := domain.WakeRate{Max: 1, Window: time.Minute}
	limiter.Allow("trg_a", oneAMinute)

	limiter.Forget("trg_a")

	if !limiter.Allow("trg_a", oneAMinute) {
		t.Fatal("firing after Forget was refused")
	}
}
