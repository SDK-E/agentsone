// SPDX-License-Identifier: MIT

package domain_test

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/SDK-E/agentsone/internal/machine/domain"
)

func TestHardLimitsHaveNoReason(t *testing.T) {
	if l := domain.HardLimits(); l.Mode != domain.LimitsHard || l.Reason != "" {
		t.Fatalf("hard limits %+v", l)
	}
}

func TestSoftLimitsAlwaysSayWhyInAtMost500Characters(t *testing.T) {
	long := strings.Repeat("The cgroup tree is read-only. ", 40)
	for name, in := range map[string]string{
		"plain": "The machine container's cgroup tree is read-only.",
		"blank": " \n\t ",
		"long":  long,
		"wide":  strings.Repeat("é", 800),
	} {
		t.Run(name, func(t *testing.T) {
			l := domain.SoftLimits(in)
			n := utf8.RuneCountInString(l.Reason)
			if l.Mode != domain.LimitsSoft || n < 1 || n > domain.MaxLimitsReason {
				t.Fatalf("soft limits %q (%d characters)", l.Reason, n)
			}
			if !utf8.ValidString(l.Reason) || strings.TrimSpace(l.Reason) != l.Reason {
				t.Fatalf("reason %q is not clean text", l.Reason)
			}
		})
	}
	// Lines stay (the contract's reason format); spaces fold and blank lines go.
	if l := domain.SoftLimits(" Why  it is.\n\nTo fix:\n-  Do\tthis. \n"); l.Reason != "Why it is.\nTo fix:\n- Do this." {
		t.Fatalf("reason %q, want lines kept and spaces folded", l.Reason)
	}
	// A long reason ends with an ellipsis after a whole word.
	l := domain.SoftLimits(long)
	kept, ok := strings.CutSuffix(l.Reason, "…")
	if !ok || !strings.HasPrefix(long, kept) || !strings.ContainsAny(long[len(kept):][:1], " .") {
		t.Fatalf("long reason %q, want it cut at a word with an ellipsis", l.Reason)
	}
}
