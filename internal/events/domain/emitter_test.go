// SPDX-License-Identifier: MIT

package domain_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/core/coretest"
	"github.com/SDK-E/agentsone/internal/events/domain"
)

func processReg(mod func(*domain.Registration)) domain.Registration {
	r := domain.Registration{
		Name: "disk-watch", Kind: domain.KindProcess, Description: "Watches disks.",
		EventTypes: []string{"agentsone.disk-watch.*"}, Command: []string{"/usr/local/bin/watch", "--fast"},
	}
	if mod != nil {
		mod(&r)
	}
	return r
}

func TestRegistration_Validate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		mod  func(*domain.Registration)
		ok   bool
	}{
		{"valid process emitter", nil, true},
		{"valid webhook emitter", func(r *domain.Registration) { r.Kind = domain.KindWebhook; r.Command = nil }, true},
		{"builtin kind cannot be registered", func(r *domain.Registration) { r.Kind = domain.KindBuiltin }, false},
		{"name with capitals", func(r *domain.Registration) { r.Name = "Disk" }, false},
		{"name over 63 characters", func(r *domain.Registration) { r.Name = "a" + strings.Repeat("b", 63) }, false},
		{"description over 500", func(r *domain.Registration) { r.Description = strings.Repeat("d", 501) }, false},
		{"no event types", func(r *domain.Registration) { r.EventTypes = nil }, false},
		{"event type outside the emitter namespace", func(r *domain.Registration) { r.EventTypes = []string{"agentsone.auth.login.failed"} }, false},
		{"event type of another emitter with the same prefix", func(r *domain.Registration) { r.EventTypes = []string{"agentsone.disk-watcher.x"} }, false},
		{"exact event type in the namespace", func(r *domain.Registration) { r.EventTypes = []string{"agentsone.disk-watch.full"} }, true},
		{"process without a command", func(r *domain.Registration) { r.Command = nil }, false},
		{"command program not absolute", func(r *domain.Registration) { r.Command = []string{"watch"} }, false},
		{"command with an empty argument", func(r *domain.Registration) { r.Command = []string{"/bin/watch", ""} }, false},
		{"webhook with a command", func(r *domain.Registration) { r.Kind = domain.KindWebhook }, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := processReg(tt.mod).Validate()
			if (err == nil) != tt.ok {
				t.Fatalf("Validate() error = %v, want ok=%v", err, tt.ok)
			}
			if err != nil && !errors.Is(err, core.ErrInvalid) {
				t.Fatalf("error %v does not wrap ErrInvalid", err)
			}
		})
	}
}

func TestNewEmitter_ProcessGetsAUnit(t *testing.T) {
	t.Parallel()
	e := domain.NewEmitter(processReg(nil), "emt_01JD7ZK3M00000000000000010", "agent:builder", coretest.Epoch)

	if e.Unit != "emitter@emt_01JD7ZK3M00000000000000010.service" || !e.Enabled || e.WebhookPath != "" {
		t.Fatalf("emitter = %+v", e)
	}
}

func TestNewEmitter_WebhookGetsAPath(t *testing.T) {
	t.Parallel()
	reg := processReg(func(r *domain.Registration) { r.Kind = domain.KindWebhook; r.Command = nil })

	e := domain.NewEmitter(reg, "emt_01JD7ZK3M00000000000000010", "agent:builder", coretest.Epoch)

	if e.WebhookPath != "/v1/webhooks/emt_01JD7ZK3M00000000000000010" || e.Unit != "" {
		t.Fatalf("emitter = %+v", e)
	}
}

func TestEmitter_Allows(t *testing.T) {
	t.Parallel()
	e := domain.Emitter{EventTypes: []string{"agentsone.ci-hooks.*", "agentsone.other.exact"}}
	if !e.Allows("agentsone.ci-hooks.build.failed") || !e.Allows("agentsone.other.exact") {
		t.Error("allowed types were refused")
	}
	if e.Allows("agentsone.other.exact2") || e.Allows("agentsone.auth.login.failed") {
		t.Error("other types were allowed")
	}
}

func TestEmitter_MayPublish(t *testing.T) {
	t.Parallel()
	owned := domain.Emitter{Owner: "agent:builder", Kind: domain.KindProcess}
	builtin := domain.Emitter{Owner: "system:daemon", Kind: domain.KindBuiltin}
	tests := []struct {
		name    string
		emitter domain.Emitter
		caller  core.Caller
		want    bool
	}{
		{"owner publishes", owned, core.Caller{Kind: core.CallerAgent, Name: "builder"}, true},
		{"other agent refused", owned, core.Caller{Kind: core.CallerAgent, Name: "scribe"}, false},
		{"system publishes for any emitter", owned, core.Caller{Kind: core.CallerSystem, Name: "daemon"}, true},
		{"host publishes for any emitter", owned, core.Caller{Kind: core.CallerHost, Name: "agentsone"}, true},
		{"agent refused for a built-in emitter", builtin, core.Caller{Kind: core.CallerAgent, Name: "builder"}, false},
		{"plugin publishes for a built-in emitter", builtin, core.Caller{Kind: core.CallerPlugin, Name: "plg_01JD7ZK3M00000000000000001"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.emitter.MayPublish(tt.caller); got != tt.want {
				t.Fatalf("MayPublish() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestBuiltinEmitterType(t *testing.T) {
	t.Parallel()
	tests := map[string]bool{
		"agentsone.disk.usage.high": true,
		"agentsone.git.ref.updated": true,
		"agentsone.builder.done":    false,
		"agentsone.disk":            false,
		"other.disk.usage.high":     false,
	}
	for eventType, want := range tests {
		if got := domain.BuiltinEmitterType(eventType); got != want {
			t.Errorf("BuiltinEmitterType(%q) = %v, want %v", eventType, got, want)
		}
	}
}

func TestBuiltinEmitters_ListTheNineFromTheSpec(t *testing.T) {
	t.Parallel()
	got := domain.BuiltinEmitters()
	want := []string{"auth", "services", "disk", "load", "files", "git", "agents", "clock", "webhooks"}
	if len(got) != len(want) {
		t.Fatalf("got %d built-in emitters, want %d", len(got), len(want))
	}
	for i, name := range want {
		if got[i].Name != name || got[i].Kind != domain.KindBuiltin || got[i].EventTypes[0] != "agentsone."+name+".*" {
			t.Errorf("built-in %d = %+v, want %s", i, got[i], name)
		}
	}
}

func TestEmitter_SignatureVerifies(t *testing.T) {
	t.Parallel()
	body := []byte(`{"a":1}`)
	sig := domain.Sign("topsecret", body)

	if !domain.VerifySignature("topsecret", body, sig) {
		t.Fatal("a correct signature was refused")
	}
	for name, bad := range map[string]string{
		"wrong secret":   domain.Sign("other", body),
		"wrong body":     domain.Sign("topsecret", []byte(`{"a":2}`)),
		"missing prefix": strings.TrimPrefix(sig, "sha256="),
		"empty":          "",
		"not hex":        "sha256=zz",
	} {
		if domain.VerifySignature("topsecret", body, bad) {
			t.Errorf("%s: a bad signature was accepted", name)
		}
	}
}
