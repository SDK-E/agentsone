// SPDX-License-Identifier: MIT

package domain_test

import (
	"testing"

	"github.com/SDK-E/agentsone/internal/mcp/domain"
)

func TestToolName_SnakeCasesTheOperationID(t *testing.T) {
	t.Parallel()
	tests := []struct{ id, want string }{
		{"hireAgent", "hire_agent"},
		{"listAgents", "list_agents"},
		{"getIdeStatus", "get_ide_status"},
		{"listPtySessions", "list_pty_sessions"},
		{"getDoorHealth", "get_door_health"},
		{"streamAPIEvents", "stream_api_events"},
		{"getMe", "get_me"},
		{"ping", "ping"},
		{"listV2Items", "list_v2_items"},
	}
	for _, tt := range tests {
		t.Run(tt.id, func(t *testing.T) {
			t.Parallel()
			if got := domain.ToolName(tt.id); got != tt.want {
				t.Fatalf("ToolName(%q) = %q, want %q", tt.id, got, tt.want)
			}
		})
	}
}

func TestExcludeFromTools_NamesTheReason(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		op   domain.Operation
		want domain.Exclusion
	}{
		{"plain operation", domain.Operation{Tag: "agents"}, domain.NotExcluded},
		{"mcp protocol", domain.Operation{Tag: "mcp"}, domain.ExcludedProtocol},
		{"host door", domain.Operation{Tag: "host"}, domain.ExcludedHostDoor},
		{"event stream", domain.Operation{Tag: "bus", Stream: true}, domain.ExcludedStream},
		{"web socket", domain.Operation{Tag: "machine", WebSocket: true}, domain.ExcludedWebSocket},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := domain.ExcludeFromTools(tt.op); got != tt.want {
				t.Fatalf("ExcludeFromTools() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestExcludeFromCommands_KeepsStreamsAndDropsDoorsAndSockets(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		op   domain.Operation
		want domain.Exclusion
	}{
		{"plain operation", domain.Operation{Tag: "agents"}, domain.NotExcluded},
		{"event stream is a command", domain.Operation{Tag: "bus", Stream: true}, domain.NotExcluded},
		{"mcp protocol is agentctl mcp", domain.Operation{Tag: "mcp"}, domain.ExcludedProtocol},
		{"host door is vsock only", domain.Operation{Tag: "host"}, domain.ExcludedHostDoor},
		{"web socket", domain.Operation{Tag: "machine", WebSocket: true}, domain.ExcludedWebSocket},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := domain.ExcludeFromCommands(tt.op); got != tt.want {
				t.Fatalf("ExcludeFromCommands() = %q, want %q", got, tt.want)
			}
		})
	}
}
