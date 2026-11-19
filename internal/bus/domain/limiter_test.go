// SPDX-License-Identifier: MIT

package domain_test

import (
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/bus/domain"
	"github.com/SDK-E/agentsone/internal/core/coretest"
)

func TestLimiter_SlidingWindow(t *testing.T) {
	t.Parallel()
	clock := coretest.NewClock(coretest.Epoch)
	l := domain.NewLimiter(clock)
	rate := domain.Rate{Max: 2, Window: time.Minute}

	if !l.Allow("a", rate) {
		t.Fatal("first send refused")
	}
	clock.Advance(30 * time.Second)
	if !l.Allow("a", rate) {
		t.Fatal("send at the limit refused")
	}
	if l.Allow("a", rate) {
		t.Fatal("send over the limit allowed")
	}
	if !l.Allow("b", rate) {
		t.Fatal("another key was limited")
	}
	clock.Advance(30 * time.Second) // the first send is exactly one window old
	if !l.Allow("a", rate) {
		t.Fatal("send at the window edge refused")
	}
	if l.Allow("a", rate) {
		t.Fatal("refusal did not hold after the window edge")
	}
	clock.Advance(time.Minute)
	if !l.Allow("a", rate) {
		t.Fatal("send after the window refused")
	}
}
