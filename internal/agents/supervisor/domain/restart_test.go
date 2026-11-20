// SPDX-License-Identifier: MIT

package domain_test

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/SDK-E/agentsone/internal/agents/supervisor/domain"
)

func TestBackoff_WaitDoublesFromTwoSecondsUpToFiveMinutes(t *testing.T) {
	t.Parallel()
	var b domain.Backoff
	var got []time.Duration
	for range 11 {
		wait, _ := b.Fail()
		got = append(got, wait)
	}
	s := time.Second
	want := []time.Duration{2 * s, 4 * s, 8 * s, 16 * s, 32 * s, 64 * s, 128 * s, 256 * s, 5 * time.Minute, 5 * time.Minute, 5 * time.Minute}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("waits (-want +got):\n%s", diff)
	}
}

func TestBackoff_OnlyTheFirstFailureBeginsASpell(t *testing.T) {
	t.Parallel()
	var b domain.Backoff
	if b.Failing() {
		t.Fatal("a new backoff is failing")
	}
	if _, began := b.Fail(); !began {
		t.Fatal("first failure did not begin a spell")
	}
	b.Up(t0) // up again, but not for long enough
	if _, began := b.Fail(); began {
		t.Fatal("second failure of the same spell began a new one")
	}
	if !b.Failing() {
		t.Fatal("backoff not failing after two failures")
	}
}

func TestBackoff_RecoversOnceTheRunnerStayedUpFiveMinutes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		up   time.Duration
		want bool
	}{
		{"just before five minutes", 5*time.Minute - time.Nanosecond, false},
		{"exactly five minutes", 5 * time.Minute, true},
		{"after five minutes", 6 * time.Minute, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var b domain.Backoff
			b.Fail()
			b.Fail()
			b.Up(t0)
			if got := b.Recovered(t0.Add(tt.up)); got != tt.want {
				t.Fatalf("Recovered() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestBackoff_ARecoveryResetsTheWaitAndEndsTheSpell(t *testing.T) {
	t.Parallel()
	var b domain.Backoff
	b.Fail()
	b.Fail()
	b.Fail()
	b.Up(t0)
	if !b.Recovered(t0.Add(domain.RestartStableAfter)) {
		t.Fatal("runner up for the stable time did not recover")
	}
	if b.Failing() {
		t.Fatal("backoff still failing after it recovered")
	}
	if b.Recovered(t0.Add(time.Hour)) {
		t.Fatal("a backoff recovered twice from one spell")
	}
	wait, began := b.Fail()
	if wait != domain.RestartFirstDelay || !began {
		t.Fatalf("next failure Fail() = %v, %v; want %v, true", wait, began, domain.RestartFirstDelay)
	}
}

func TestBackoff_DoesNotRecoverWithoutComingUp(t *testing.T) {
	t.Parallel()
	var b domain.Backoff
	if b.Recovered(t0) {
		t.Fatal("a backoff that never failed recovered")
	}
	b.Fail()
	if b.Recovered(t0.Add(time.Hour)) {
		t.Fatal("a failing runner that never came up recovered")
	}
	b.Up(t0)
	b.Fail() // down again before the stable time
	if b.Recovered(t0.Add(time.Hour)) {
		t.Fatal("a runner that failed again after coming up recovered")
	}
}
