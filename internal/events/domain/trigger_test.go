// SPDX-License-Identifier: MIT

package domain_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/core/coretest"
	"github.com/SDK-E/agentsone/internal/events/domain"
)

func wakeSpec(mod func(*domain.TriggerSpec)) domain.TriggerSpec {
	s := domain.TriggerSpec{
		Name:    "disk alarm",
		Filter:  domain.Filter{Types: []string{"agentsone.disk.*"}},
		Action:  domain.Action{Kind: domain.ActionWake, Agent: "janitor", Message: "Disk event {{type}} on {{subject}}"},
		Rate:    domain.TriggerRate{Max: 2, PerSeconds: 60},
		Enabled: true,
	}
	if mod != nil {
		mod(&s)
	}
	return s
}

func TestTriggerSpec_Validate(t *testing.T) {
	t.Parallel()
	task := domain.Action{Kind: domain.ActionCreateTask, Agent: "janitor", Title: "Clean {{subject}}", Priority: "high"}
	tests := []struct {
		name string
		mod  func(*domain.TriggerSpec)
		ok   bool
	}{
		{"valid wake trigger", nil, true},
		{"valid task trigger", func(s *domain.TriggerSpec) { s.Action = task }, true},
		{"empty name", func(s *domain.TriggerSpec) { s.Name = "" }, false},
		{"name at 80", func(s *domain.TriggerSpec) { s.Name = strings.Repeat("n", 80) }, true},
		{"name over 80", func(s *domain.TriggerSpec) { s.Name = strings.Repeat("n", 81) }, false},
		{"filter with no field would match every event", func(s *domain.TriggerSpec) { s.Filter = domain.Filter{} }, false},
		{"filter by source only", func(s *domain.TriggerSpec) { s.Filter = domain.Filter{Sources: []string{"/emitters/disk"}} }, true},
		{"unknown action kind", func(s *domain.TriggerSpec) { s.Action.Kind = "email" }, false},
		{"wake without a target", func(s *domain.TriggerSpec) { s.Action.Agent = "" }, false},
		{"wake target with capitals", func(s *domain.TriggerSpec) { s.Action.Agent = "Janitor" }, false},
		{"wake message over 2000", func(s *domain.TriggerSpec) { s.Action.Message = strings.Repeat("m", 2001) }, false},
		{"task without a title", func(s *domain.TriggerSpec) {
			s.Action = domain.Action{Kind: domain.ActionCreateTask, Agent: "janitor", Priority: "low"}
		}, false},
		{"task title over 200", func(s *domain.TriggerSpec) {
			s.Action = domain.Action{Kind: domain.ActionCreateTask, Agent: "janitor", Title: strings.Repeat("t", 201), Priority: "low"}
		}, false},
		{"task with an unknown priority", func(s *domain.TriggerSpec) {
			s.Action.Kind = domain.ActionCreateTask
			s.Action.Title = "x"
			s.Action.Priority = "urgent"
		}, false},
		{"rate max 1", func(s *domain.TriggerSpec) { s.Rate.Max = 1 }, true},
		{"rate max 0", func(s *domain.TriggerSpec) { s.Rate.Max = 0 }, false},
		{"rate max 1000", func(s *domain.TriggerSpec) { s.Rate.Max = 1000 }, true},
		{"rate max 1001", func(s *domain.TriggerSpec) { s.Rate.Max = 1001 }, false},
		{"rate window 0", func(s *domain.TriggerSpec) { s.Rate.PerSeconds = 0 }, false},
		{"rate window one day", func(s *domain.TriggerSpec) { s.Rate.PerSeconds = 86400 }, true},
		{"rate window over a day", func(s *domain.TriggerSpec) { s.Rate.PerSeconds = 86401 }, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := wakeSpec(tt.mod).Validate()
			if (err == nil) != tt.ok {
				t.Fatalf("Validate() error = %v, want ok=%v", err, tt.ok)
			}
			if err != nil && !errors.Is(err, core.ErrInvalid) {
				t.Fatalf("error %v does not wrap ErrInvalid", err)
			}
		})
	}
}

func TestTrigger_Matches(t *testing.T) {
	t.Parallel()
	trigger := domain.NewTrigger(wakeSpec(nil), "trg_1", "janitor", coretest.Epoch)

	if !trigger.Matches(sample(nil)) {
		t.Error("an enabled trigger ignored a matching event")
	}
	if trigger.Matches(sample(func(e *core.Event) { e.Type = "agentsone.git.ref.updated" })) {
		t.Error("a trigger matched an event outside its filter")
	}
	trigger.Enabled = false
	if trigger.Matches(sample(nil)) {
		t.Error("a disabled trigger matched")
	}
}

func TestTrigger_PatchChangesOnlyTheFieldsGiven(t *testing.T) {
	t.Parallel()
	trigger := domain.NewTrigger(wakeSpec(nil), "trg_1", "janitor", coretest.Epoch)
	trigger.FiredCount = 7
	off := false

	got, err := trigger.Apply(domain.TriggerPatch{Enabled: &off})

	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if got.Enabled || got.Name != "disk alarm" || got.FiredCount != 7 {
		t.Fatalf("patched trigger = %+v", got)
	}
}

func TestTrigger_PatchIsValidated(t *testing.T) {
	t.Parallel()
	trigger := domain.NewTrigger(wakeSpec(nil), "trg_1", "janitor", coretest.Epoch)
	zero := domain.TriggerRate{Max: 0, PerSeconds: 60}

	if _, err := trigger.Apply(domain.TriggerPatch{Rate: &zero}); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("Apply() error = %v, want ErrInvalid", err)
	}
}

func TestRender_FillsPlaceholdersFromTheEvent(t *testing.T) {
	t.Parallel()
	got := domain.Render("{{type}} at {{subject}} ({{id}}) {{other}}", sample(nil))

	want := "agentsone.disk.usage.high at /var (evt_1) {{other}}"
	if got != want {
		t.Fatalf("Render() = %q, want %q", got, want)
	}
}

func TestTriggerRate_Rate(t *testing.T) {
	t.Parallel()
	got := domain.TriggerRate{Max: 3, PerSeconds: 90}.Rate()

	if got.Max != 3 || got.Window != 90*time.Second {
		t.Fatalf("Rate() = %+v", got)
	}
}

func TestViewer_CanSee(t *testing.T) {
	t.Parallel()
	below := func(name string) bool { return name == "intern" }
	member := domain.Viewer{Name: "builder", TeamRoot: "founder", IsBelow: below}
	tests := []struct {
		name   string
		viewer domain.Viewer
		scope  string
		want   bool
	}{
		{"machine scope is public", member, "machine", true},
		{"own team", member, "team:founder", true},
		{"other team", member, "team:other", false},
		{"own agent scope", member, "agent:builder", true},
		{"agent below the viewer", member, "agent:intern", true},
		{"agent elsewhere", member, "agent:stranger", false},
		{"unrestricted viewer sees a team", domain.Viewer{All: true}, "team:other", true},
		{"unrestricted viewer sees an agent", domain.Viewer{All: true}, "agent:stranger", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.viewer.CanSee(sample(func(e *core.Event) { e.ChartScope = tt.scope })); got != tt.want {
				t.Fatalf("CanSee(%s) = %v, want %v", tt.scope, got, tt.want)
			}
		})
	}
}

func TestViewer_CanSeeAgent(t *testing.T) {
	t.Parallel()
	v := domain.Viewer{Name: "builder", IsBelow: func(name string) bool { return name == "intern" }}
	if !v.CanSeeAgent("builder") || !v.CanSeeAgent("intern") || v.CanSeeAgent("stranger") {
		t.Fatal("CanSeeAgent allowed the wrong agents")
	}
	if !(domain.Viewer{All: true}).CanSeeAgent("anyone") {
		t.Fatal("an unrestricted viewer cannot see an agent")
	}
}
