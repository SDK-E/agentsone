// SPDX-License-Identifier: MIT

package domain

import (
	"strings"
	"unicode/utf8"
)

// LimitsMode says whether the Linux kernel holds custom agents' limits.
type LimitsMode string

// Limits modes. The zero value means the daemon was not told.
const (
	LimitsUnknown LimitsMode = ""
	LimitsHard    LimitsMode = "hard"
	LimitsSoft    LimitsMode = "soft"
)

// MaxLimitsReason is the longest reason, in characters, the API carries.
const MaxLimitsReason = 500

// softFallback is the reason of soft limits that came with none.
const softFallback = "The machine could not give each custom agent a cgroup of its own when it started, so the kernel does not hold their limits."

// AgentLimits is how custom agents' limits hold on this machine, decided
// once when the daemon started. The zero value is unknown: the metrics
// leave it out.
type AgentLimits struct {
	Mode LimitsMode
	// Reason says, in plain words, why the limits are soft and how to make
	// them hard. Always empty when hard, never empty when soft.
	Reason string
}

// HardLimits are limits the kernel holds.
func HardLimits() AgentLimits { return AgentLimits{Mode: LimitsHard} }

// SoftLimits are limits the kernel does not hold, for reason. A blank
// reason becomes a general one and a long one is cut at a word to
// MaxLimitsReason characters, so the API always has 1 to 500.
func SoftLimits(reason string) AgentLimits {
	reason = strings.Join(strings.Fields(reason), " ")
	if reason == "" {
		reason = softFallback
	}
	if utf8.RuneCountInString(reason) > MaxLimitsReason {
		reason = cut(reason, MaxLimitsReason)
	}
	return AgentLimits{Mode: LimitsSoft, Reason: reason}
}

// cut shortens s to at most n characters, ending with an ellipsis at a word
// boundary when there is one.
func cut(s string, n int) string {
	r := []rune(s)[:n-1]
	out := string(r)
	if i := strings.LastIndexByte(out, ' '); i > 0 {
		out = out[:i]
	}
	return strings.TrimRight(out, " ,.;:") + "…"
}
