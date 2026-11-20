// SPDX-License-Identifier: MIT

package domain_test

import (
	"testing"

	"github.com/SDK-E/agentsone/internal/emitkit/domain"
)

func TestGate_Observe(t *testing.T) {
	t.Parallel()
	steps := []struct {
		reading int
		want    domain.Transition
	}{
		{50, domain.Steady},
		{89, domain.Steady},
		{90, domain.WentHigh},
		{95, domain.Steady},
		{89, domain.Steady}, // inside the margin: still high
		{88, domain.Steady},
		{87, domain.WentNormal},
		{89, domain.Steady},
		{91, domain.WentHigh},
	}
	g := domain.NewGate(90, 2)
	for i, s := range steps {
		if got := g.Observe(s.reading); got != s.want {
			t.Fatalf("step %d: Observe(%d) = %v, want %v", i, s.reading, got, s.want)
		}
	}
}

func TestGate_StartingHighIsATransition(t *testing.T) {
	t.Parallel()
	g := domain.NewGate(90, 2)
	if got := g.Observe(97); got != domain.WentHigh {
		t.Fatalf("first reading 97 = %v, want WentHigh", got)
	}
	if !g.High() {
		t.Fatal("High() = false after going high")
	}
}

func TestGate_StartingNormalIsSilent(t *testing.T) {
	t.Parallel()
	g := domain.NewGate(90, 2)
	if got := g.Observe(10); got != domain.Steady {
		t.Fatalf("first reading 10 = %v, want Steady", got)
	}
}

func TestGate_NegativeMarginCountsAsZero(t *testing.T) {
	t.Parallel()
	g := domain.NewGate(50, -5)
	g.Observe(60)
	if got := g.Observe(49); got != domain.WentNormal {
		t.Fatalf("Observe(49) = %v, want WentNormal", got)
	}
}

func TestSustainedGate_NeedsConsecutiveHighReadings(t *testing.T) {
	t.Parallel()
	steps := []struct {
		reading int
		want    domain.Transition
	}{
		{95, domain.Steady}, // 1 of 3
		{96, domain.Steady}, // 2 of 3
		{50, domain.Steady}, // a dip resets the count
		{95, domain.Steady},
		{95, domain.Steady},
		{95, domain.WentHigh}, // 3 in a row
		{95, domain.Steady},
		{40, domain.WentNormal},
	}
	g := domain.NewSustainedGate(90, 5, 3)
	for i, s := range steps {
		if got := g.Observe(s.reading); got != s.want {
			t.Fatalf("step %d: Observe(%d) = %v, want %v", i, s.reading, got, s.want)
		}
	}
}

func TestSustainedGate_ReadingInsideTheMarginKeepsItHigh(t *testing.T) {
	t.Parallel()
	g := domain.NewSustainedGate(90, 5, 2)
	g.Observe(95)
	g.Observe(95)
	if got := g.Observe(86); got != domain.Steady || !g.High() {
		t.Fatalf("Observe(86) = %v, High = %v; want steady and still high", got, g.High())
	}
}

func TestSustainedGate_SustainBelowOneCountsAsOne(t *testing.T) {
	t.Parallel()
	if got := domain.NewSustainedGate(90, 0, 0).Observe(91); got != domain.WentHigh {
		t.Fatalf("Observe(91) = %v, want WentHigh", got)
	}
}

func TestTransition_String(t *testing.T) {
	t.Parallel()
	for tr, want := range map[domain.Transition]string{
		domain.Steady: "steady", domain.WentHigh: "went-high", domain.WentNormal: "went-normal",
	} {
		if got := tr.String(); got != want {
			t.Errorf("%d.String() = %q, want %q", tr, got, want)
		}
	}
}
