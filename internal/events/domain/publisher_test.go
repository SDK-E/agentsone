// SPDX-License-Identifier: MIT

package domain_test

import (
	"errors"
	"testing"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/events/domain"
)

var (
	tokenFounder = core.Caller{Kind: core.CallerAgent, Name: "founder", UID: -1, Assurance: core.AssuranceSoft}
	rootFounder  = core.Caller{Kind: core.CallerAgent, Name: "founder", UID: 0, IsRoot: true}
	agentsModule = core.Caller{Kind: core.CallerSystem, Name: "agents", UID: -1}
	hostCaller   = core.Caller{Kind: core.CallerHost, Name: "agentsone", UID: -1}
	human        = core.Caller{Kind: core.CallerHuman, Name: "ada", UID: 1000}
	plugin       = core.Caller{Kind: core.CallerPlugin, Name: "plg_01JD7ZK3M00000000000000001", UID: -1}
)

func TestAuthoriseSource(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		caller    core.Caller
		source    string
		eventType string
		want      error
	}{
		{"an agent's own type on its own source", tokenFounder, "/agents/founder", "agentsone.founder.done", nil},
		{"a token agent may not publish a built-in type itself", tokenFounder, "/agents/founder", "agentsone.agents.hired", domain.ErrTypeReserved},
		{"a custom agent may not publish a built-in type itself", core.Caller{Kind: core.CallerAgent, Name: "lead", UID: 1100}, "/agents/lead", "agentsone.agents.retired", domain.ErrTypeReserved},
		{"a human may not publish a built-in type", human, "/daemon", "agentsone.auth.login.succeeded", domain.ErrTypeReserved},
		{"a plugin may not publish a built-in type outside an emitter", plugin, "/daemon", "agentsone.disk.usage.high", domain.ErrTypeReserved},
		{"another agent's source", tokenFounder, "/agents/lead", "agentsone.founder.done", domain.ErrSourceForbidden},
		{"an agent may not pose as the daemon", tokenFounder, "/daemon", "agentsone.founder.done", domain.ErrSourceForbidden},
		{"a human on the daemon source", human, "/daemon", "agentsone.notes.saved", nil},
		{"a daemon module announces a built-in fact on the hirer's source", agentsModule, "/agents/founder", "agentsone.agents.hired", nil},
		{"a root agent over the socket", rootFounder, "/agents/founder", "agentsone.agents.hired", nil},
		{"the host", hostCaller, "/host", "agentsone.agents.hired", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if err := domain.AuthoriseSource(tt.caller, tt.source, tt.eventType); !errors.Is(err, tt.want) {
				t.Fatalf("AuthoriseSource() = %v, want %v", err, tt.want)
			}
		})
	}
}

// Only a daemon module names who caused the fact it announces. Anyone else
// is the actor of what it publishes, whatever the event says.
func TestPortActor(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		caller  core.Caller
		named   string
		want    string
		wantErr error
	}{
		{"a module names the agent that hired", agentsModule, "agent:founder", "agent:founder", nil},
		{"a module that names no one is the actor", agentsModule, "", "system:agents", nil},
		{"a module names a plugin", agentsModule, "plugin:plg_01JD7ZK3M00000000000000001", "plugin:plg_01JD7ZK3M00000000000000001", nil},
		{"a module may not name a malformed actor", agentsModule, "agent:Founder!", "", domain.ErrInvalidActor},
		{"a module may not name an unknown kind", agentsModule, "root:founder", "", domain.ErrInvalidActor},
		{"a token agent that names the system is itself", tokenFounder, "system:auth", "agent:founder", nil},
		{"a root agent that names another agent is itself", rootFounder, "agent:lead", "agent:founder", nil},
		{"the host that names an agent is itself", hostCaller, "agent:lead", "host:agentsone", nil},
		{"a human that names the system is itself", human, "system:daemon", "human:ada", nil},
		{"a plugin that names an agent is the plugin", plugin, "agent:lead", "system:plugin", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := domain.PortActor(tt.caller, tt.named)
			if !errors.Is(err, tt.wantErr) || got != tt.want {
				t.Fatalf("PortActor() = %q, %v, want %q, %v", got, err, tt.want, tt.wantErr)
			}
		})
	}
}
