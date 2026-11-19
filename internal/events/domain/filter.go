// SPDX-License-Identifier: MIT

package domain

import (
	"slices"
	"strings"
	"time"

	"github.com/SDK-E/agentsone/internal/core"
)

// Filter selects events. Every field that is set must match.
type Filter struct {
	Types         []string // patterns, see MatchPattern
	Sources       []string // patterns
	SubjectPrefix string
	Actors        []string // exact "<kind>:<name>"
	ChartScope    string
	// AfterSequence keeps events whose sequence is larger ("" keeps all).
	AfterSequence string
	Since, Until  *time.Time
}

// Matches reports whether e passes every set field.
func (f Filter) Matches(e core.Event) bool {
	if f.AfterSequence != "" && e.Sequence <= f.AfterSequence {
		return false
	}
	if !MatchAny(f.Types, e.Type) || !MatchAny(f.Sources, e.Source) {
		return false
	}
	if f.SubjectPrefix != "" && !strings.HasPrefix(e.Subject, f.SubjectPrefix) {
		return false
	}
	if len(f.Actors) > 0 && !slices.Contains(f.Actors, e.Actor) {
		return false
	}
	if f.ChartScope != "" && f.ChartScope != e.ChartScope {
		return false
	}
	return inRange(e.Time, f.Since, f.Until)
}

// IsEmpty reports whether f has no field that narrows events.
func (f Filter) IsEmpty() bool {
	return len(f.Types) == 0 && len(f.Sources) == 0 && f.SubjectPrefix == "" && len(f.Actors) == 0 &&
		f.ChartScope == "" && f.Since == nil && f.Until == nil
}

func inRange(at, since, until *time.Time) bool {
	if since == nil && until == nil {
		return true
	}
	if at == nil {
		return false
	}
	if since != nil && at.Before(*since) {
		return false
	}
	return until == nil || !at.After(*until)
}
