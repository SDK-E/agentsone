// SPDX-License-Identifier: MIT

package domain_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/core/coretest"
	"github.com/SDK-E/agentsone/internal/events/domain"
)

func validDraft(mod func(*domain.Draft)) domain.Draft {
	d := domain.Draft{Type: "agentsone.agents.report-ready", Data: json.RawMessage(`{"title":"x"}`)}
	if mod != nil {
		mod(&d)
	}
	return d
}

func TestDraft_Validate(t *testing.T) {
	t.Parallel()
	bigData := `{"x":"` + strings.Repeat("a", domain.MaxDataBytes) + `"}`
	exactData := `{"x":"` + strings.Repeat("a", domain.MaxDataBytes-8) + `"}`
	tests := []struct {
		name    string
		mod     func(*domain.Draft)
		wantErr error
	}{
		{"valid draft", nil, nil},
		{"type without the agentsone prefix", func(d *domain.Draft) { d.Type = "disk.usage.high" }, domain.ErrInvalidType},
		{"type with one segment", func(d *domain.Draft) { d.Type = "agentsone.disk" }, domain.ErrInvalidType},
		{"type with capital letters", func(d *domain.Draft) { d.Type = "agentsone.Disk.high" }, domain.ErrInvalidType},
		{"type over 128 characters", func(d *domain.Draft) { d.Type = "agentsone.a." + strings.Repeat("b", 120) }, domain.ErrInvalidType},
		{"data missing", func(d *domain.Draft) { d.Data = nil }, domain.ErrInvalidData},
		{"data is an array", func(d *domain.Draft) { d.Data = json.RawMessage(`[1]`) }, domain.ErrInvalidData},
		{"data is not json", func(d *domain.Draft) { d.Data = json.RawMessage(`{`) }, domain.ErrInvalidData},
		{"data exactly at the limit", func(d *domain.Draft) { d.Data = json.RawMessage(exactData) }, nil},
		{"data over the limit", func(d *domain.Draft) { d.Data = json.RawMessage(bigData) }, domain.ErrTooLarge},
		{"subject at 1024", func(d *domain.Draft) { d.Subject = strings.Repeat("s", 1024) }, nil},
		{"subject over 1024", func(d *domain.Draft) { d.Subject = strings.Repeat("s", 1025) }, domain.ErrInvalidSubject},
		{"causation is a prefixed id", func(d *domain.Draft) { d.CausationID = "evt_01JD7ZK3M00000000000000007" }, nil},
		{"causation is not an id", func(d *domain.Draft) { d.CausationID = "because" }, domain.ErrInvalidLink},
		{"correlation is not an id", func(d *domain.Draft) { d.CorrelationID = "chain-1" }, domain.ErrInvalidLink},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validDraft(tt.mod).Validate()
			if !errors.Is(err, tt.wantErr) && !(tt.wantErr == nil && err == nil) {
				t.Fatalf("Validate() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestActorOf(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		caller  core.Caller
		want    string
		wantErr error
	}{
		{"agent", core.Caller{Kind: core.CallerAgent, Name: "scribe"}, "agent:scribe", nil},
		{"human", core.Caller{Kind: core.CallerHuman, Name: "ada"}, "human:ada", nil},
		{"system", core.Caller{Kind: core.CallerSystem, Name: "daemon"}, "system:daemon", nil},
		{"agent without a name", core.Caller{Kind: core.CallerAgent}, "", domain.ErrInvalidActor},
		{"name with a bad character", core.Caller{Kind: core.CallerAgent, Name: "Bad Name"}, "", domain.ErrInvalidActor},
		{"plugin id as name", core.Caller{Kind: core.CallerPlugin, Name: "plg_01JD7ZK3M00000000000000001"}, "plugin:plg_01JD7ZK3M00000000000000001", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := domain.ActorOf(tt.caller)
			if !errors.Is(err, tt.wantErr) && !(tt.wantErr == nil && err == nil) {
				t.Fatalf("ActorOf() error = %v, want %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("ActorOf() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestEventActor_WritesPluginsAsSystem(t *testing.T) {
	t.Parallel()
	got, err := domain.EventActor(core.Caller{Kind: core.CallerPlugin, Name: "plg_01JD7ZK3M00000000000000001"})
	if err != nil || got != "system:plugin" {
		t.Fatalf("EventActor() = %q, %v, want system:plugin", got, err)
	}
	if got, _ := domain.EventActor(core.Caller{Kind: core.CallerAgent, Name: "scribe"}); got != "agent:scribe" {
		t.Fatalf("EventActor(agent) = %q", got)
	}
}

func TestDefaultSource(t *testing.T) {
	t.Parallel()
	tests := []struct {
		caller core.Caller
		want   string
	}{
		{core.Caller{Kind: core.CallerAgent, Name: "scribe"}, "/agents/scribe"},
		{core.Caller{Kind: core.CallerHost, Name: "agentsone"}, "/host"},
		{core.Caller{Kind: core.CallerSystem, Name: "daemon"}, "/daemon"},
		{core.Caller{Kind: core.CallerHuman, Name: "ada"}, "/daemon"},
	}
	for _, tt := range tests {
		if got := domain.DefaultSource(tt.caller); got != tt.want {
			t.Errorf("DefaultSource(%s) = %q, want %q", tt.caller.Kind, got, tt.want)
		}
	}
}

func TestEmitterOfSource(t *testing.T) {
	t.Parallel()
	if name, ok := domain.EmitterOfSource("/emitters/ci-hooks"); !ok || name != "ci-hooks" {
		t.Errorf("emitter source = %q, %v", name, ok)
	}
	if name, ok := domain.EmitterOfSource("/emitters/ci-hooks/job-1"); !ok || name != "ci-hooks" {
		t.Errorf("emitter source with path = %q, %v", name, ok)
	}
	if _, ok := domain.EmitterOfSource("/agents/scribe"); ok {
		t.Error("agent source reported as an emitter")
	}
}

func TestValidSource(t *testing.T) {
	t.Parallel()
	for _, ok := range []string{"/daemon", "/host", "/agents/scribe", "/emitters/disk", "/emitters/disk/sda1"} {
		if err := domain.ValidateSource(ok); err != nil {
			t.Errorf("ValidateSource(%q) = %v", ok, err)
		}
	}
	for _, bad := range []string{"", "daemon", "/agents/Scribe", "/emitters/", "/other", "/agents/x y"} {
		if err := domain.ValidateSource(bad); !errors.Is(err, domain.ErrInvalidSource) {
			t.Errorf("ValidateSource(%q) = %v, want ErrInvalidSource", bad, err)
		}
	}
}

func TestNewEvent_FillsTheEnvelope(t *testing.T) {
	t.Parallel()
	d := validDraft(func(d *domain.Draft) { d.Source = "/agents/scribe"; d.Subject = "mem_1" })

	e := d.Event("evt_01JD7ZK3M00000000000000007", coretest.Epoch, "agent:scribe", "team:builder")

	if e.SpecVersion != "1.0" || e.DataContentType != "application/json" {
		t.Errorf("envelope = %+v", e)
	}
	if e.Actor != "agent:scribe" || e.ChartScope != "team:builder" || e.Source != "/agents/scribe" || e.Subject != "mem_1" {
		t.Errorf("fields = %+v", e)
	}
	if e.Time == nil || !e.Time.Equal(coretest.Epoch) {
		t.Errorf("time = %v", e.Time)
	}
}

func TestNewEvent_CorrelationDefaults(t *testing.T) {
	t.Parallel()
	const id = "evt_01JD7ZK3M00000000000000007"
	const cause = "tsk_01JD7ZK3M00000000000000001"
	tests := []struct {
		name string
		mod  func(*domain.Draft)
		want string
	}{
		{"first event of a chain correlates with itself", nil, id},
		{"caused event correlates with its cause", func(d *domain.Draft) { d.CausationID = cause }, cause},
		{"explicit correlation wins", func(d *domain.Draft) {
			d.CausationID = cause
			d.CorrelationID = "msg_01JD7ZK3M00000000000000002"
		}, "msg_01JD7ZK3M00000000000000002"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			e := validDraft(tt.mod).Event(id, coretest.Epoch, "agent:a", "machine")
			if e.CorrelationID != tt.want {
				t.Fatalf("correlationid = %q, want %q", e.CorrelationID, tt.want)
			}
		})
	}
}
