// SPDX-License-Identifier: MIT

package domain_test

import (
	"errors"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/core/coretest"
	"github.com/SDK-E/agentsone/internal/events/domain"
)

func sample(mod func(*core.Event)) core.Event {
	at := coretest.Epoch
	e := core.Event{
		SpecVersion: "1.0", ID: "evt_1", Source: "/emitters/disk", Type: "agentsone.disk.usage.high",
		Subject: "/var", Time: &at, Sequence: core.FormatSequence(5), Actor: "system:disk", ChartScope: "machine",
	}
	if mod != nil {
		mod(&e)
	}
	return e
}

func TestMatchPattern(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, pattern, value string
		want                 bool
	}{
		{"exact", "agentsone.disk.usage.high", "agentsone.disk.usage.high", true},
		{"exact differs", "agentsone.disk.usage.high", "agentsone.disk.usage.normal", false},
		{"star matches everything below", "agentsone.disk.*", "agentsone.disk.usage.high", true},
		{"star needs the dot", "agentsone.disk.*", "agentsone.diskette.full", false},
		{"lone star matches all", "*", "agentsone.x.y", true},
		{"trailing dot is a prefix", "agentsone.git.", "agentsone.git.ref.updated", true},
		{"trailing dot other prefix", "agentsone.git.", "agentsone.gitlab.x", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := domain.MatchPattern(tt.pattern, tt.value); got != tt.want {
				t.Fatalf("MatchPattern(%q, %q) = %v, want %v", tt.pattern, tt.value, got, tt.want)
			}
		})
	}
}

func TestFilter_Matches(t *testing.T) {
	t.Parallel()
	later := coretest.Epoch.Add(time.Hour)
	earlier := coretest.Epoch.Add(-time.Hour)
	tests := []struct {
		name   string
		filter domain.Filter
		want   bool
	}{
		{"empty filter matches", domain.Filter{}, true},
		{"type pattern matches", domain.Filter{Types: []string{"agentsone.disk.*"}}, true},
		{"type pattern misses", domain.Filter{Types: []string{"agentsone.git.*"}}, false},
		{"any of several types", domain.Filter{Types: []string{"agentsone.git.*", "agentsone.disk.*"}}, true},
		{"source pattern matches", domain.Filter{Sources: []string{"/emitters/*"}}, true},
		{"source misses", domain.Filter{Sources: []string{"/agents/*"}}, false},
		{"subject prefix matches", domain.Filter{SubjectPrefix: "/v"}, true},
		{"subject prefix misses", domain.Filter{SubjectPrefix: "/home"}, false},
		{"actor matches", domain.Filter{Actors: []string{"system:disk"}}, true},
		{"actor misses", domain.Filter{Actors: []string{"agent:scribe"}}, false},
		{"chart scope matches", domain.Filter{ChartScope: "machine"}, true},
		{"chart scope misses", domain.Filter{ChartScope: "team:builder"}, false},
		{"after sequence below keeps", domain.Filter{AfterSequence: core.FormatSequence(4)}, true},
		{"after the same sequence drops", domain.Filter{AfterSequence: core.FormatSequence(5)}, false},
		{"since in the past keeps", domain.Filter{Since: &earlier}, true},
		{"since in the future drops", domain.Filter{Since: &later}, false},
		{"until in the future keeps", domain.Filter{Until: &later}, true},
		{"until in the past drops", domain.Filter{Until: &earlier}, false},
		{"until on the event time keeps", domain.Filter{Until: &coretest.Epoch}, true},
		{"all set fields must match", domain.Filter{Types: []string{"agentsone.disk.*"}, Actors: []string{"agent:x"}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.filter.Matches(sample(nil)); got != tt.want {
				t.Fatalf("Matches() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestFilter_TimeFiltersDropEventsWithoutATime(t *testing.T) {
	t.Parallel()
	since := coretest.Epoch
	e := sample(func(e *core.Event) { e.Time = nil })

	if (domain.Filter{Since: &since}).Matches(e) {
		t.Fatal("event without time passed a time filter")
	}
}

func TestFilter_IsEmpty(t *testing.T) {
	t.Parallel()
	if !(domain.Filter{AfterSequence: "1"}).IsEmpty() {
		t.Error("a filter with only a sequence does not narrow events")
	}
	if (domain.Filter{Sources: []string{"/daemon"}}).IsEmpty() {
		t.Error("a filter with a source narrows events")
	}
}

func TestParseSequence(t *testing.T) {
	t.Parallel()
	if n, err := domain.ParseSequence(""); err != nil || n != 0 {
		t.Errorf("empty sequence = %d, %v", n, err)
	}
	if n, err := domain.ParseSequence("00000000000000000042"); err != nil || n != 42 {
		t.Errorf("padded sequence = %d, %v", n, err)
	}
	if _, err := domain.ParseSequence("x"); !errors.Is(err, core.ErrInvalid) {
		t.Errorf("bad sequence error = %v, want ErrInvalid", err)
	}
}
