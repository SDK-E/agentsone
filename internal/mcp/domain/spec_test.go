// SPDX-License-Identifier: MIT

package domain_test

import (
	"errors"
	"testing"

	"github.com/SDK-E/agentsone/internal/mcp/domain"
)

const miniSpec = `{
  "openapi": "3.1.0",
  "servers": [{"url": "http://127.0.0.1:7700/v1"}],
  "paths": {
    "/agents/{agent_name}": {
      "get": {
        "operationId": "getAgent", "tags": ["agents"], "summary": "Get an agent", "description": "Reads one agent.",
        "parameters": [
          {"$ref": "#/components/parameters/AgentName"},
          {"name": "verbose", "in": "query", "schema": {"type": "boolean"}},
          {"name": "Mcp-Session-Id", "in": "header", "schema": {"type": "string"}}
        ],
        "responses": {
          "200": {"description": "ok", "content": {"application/json": {"schema": {"$ref": "#/components/schemas/Agent"}}}},
          "404": {"description": "missing"}
        }
      },
      "delete": {
        "operationId": "retireAgent", "tags": ["agents"], "summary": "Retire",
        "parameters": [{"$ref": "#/components/parameters/AgentName"}],
        "responses": {"204": {"description": "gone"}}
      }
    },
    "/agents": {
      "post": {
        "operationId": "hireAgent", "tags": ["agents"], "summary": "Hire",
        "requestBody": {"required": true, "content": {"application/json": {"schema": {"$ref": "#/components/schemas/AgentCreate"}}}},
        "responses": {"201": {"description": "hired", "content": {"application/json": {"schema": {"$ref": "#/components/schemas/Agent"}}}}}
      }
    },
    "/agents/{agent_name}/output": {
      "get": {
        "operationId": "streamAgentOutput", "tags": ["agents"], "summary": "Follow output",
        "x-stream": {"protocol": "sse", "frame": "Frame"},
        "parameters": [{"$ref": "#/components/parameters/AgentName"}],
        "responses": {"200": {"description": "stream", "content": {"text/event-stream": {"schema": {"type": "object"}}}}}
      }
    },
    "/machine/pty/{pty_id}/socket": {
      "get": {
        "operationId": "connectPty", "tags": ["machine"], "summary": "Attach",
        "x-websocket": {"subprotocol": "x"},
        "parameters": [{"name": "pty_id", "in": "path", "required": true, "schema": {"type": "string"}}],
        "responses": {"101": {"description": "switching"}}
      }
    }
  },
  "components": {
    "parameters": {"AgentName": {"name": "agent_name", "in": "path", "required": true, "description": "Agent name.", "schema": {"type": "string"}}},
    "schemas": {
      "Agent": {"type": "object", "properties": {"name": {"type": "string"}, "parent": {"$ref": "#/components/schemas/Agent"}}},
      "AgentCreate": {"type": "object", "required": ["name"], "properties": {"name": {"type": "string"}}}
    }
  }
}`

func parseMini(t *testing.T) map[string]domain.Operation {
	t.Helper()
	ops, err := domain.ParseOperations([]byte(miniSpec))
	if err != nil {
		t.Fatalf("ParseOperations() error = %v", err)
	}
	byID := map[string]domain.Operation{}
	for _, op := range ops {
		byID[op.ID] = op
	}
	return byID
}

func TestParseOperations_ReadsEveryOperation(t *testing.T) {
	t.Parallel()
	ops := parseMini(t)
	if len(ops) != 5 {
		t.Fatalf("got %d operations, want 5", len(ops))
	}
}

func TestParseOperations_PrefixesBasePathAndSplitsParams(t *testing.T) {
	t.Parallel()
	op := parseMini(t)["getAgent"]

	if op.Path != "/v1/agents/{agent_name}" || op.Method != "GET" || op.Tag != "agents" {
		t.Fatalf("op = %s %s tag %s", op.Method, op.Path, op.Tag)
	}
	if len(op.Params) != 2 {
		t.Fatalf("params = %+v, want path and query only (header dropped)", op.Params)
	}
	if p := op.Params[0]; p.Name != "agent_name" || p.In != domain.InPath || !p.Required || p.Description != "Agent name." {
		t.Errorf("first param = %+v", p)
	}
	if p := op.Params[1]; p.Name != "verbose" || p.In != domain.InQuery || p.Required {
		t.Errorf("second param = %+v", p)
	}
}

func TestParseOperations_ReadsSuccessSchemaAndSummary(t *testing.T) {
	t.Parallel()
	op := parseMini(t)["getAgent"]

	if op.Summary != "Get an agent" || op.Description != "Reads one agent." {
		t.Errorf("summary %q description %q", op.Summary, op.Description)
	}
	if op.Success == nil || op.Success.Status != 200 || op.Success.Schema == nil {
		t.Fatalf("success = %+v", op.Success)
	}
}

func TestParseOperations_NoJSONBodyMeansNilSchema(t *testing.T) {
	t.Parallel()
	op := parseMini(t)["retireAgent"]

	if op.Success == nil || op.Success.Status != 204 || op.Success.Schema != nil {
		t.Fatalf("success = %+v, want 204 without schema", op.Success)
	}
}

func TestParseOperations_CutsRecursiveReferences(t *testing.T) {
	t.Parallel()
	op := parseMini(t)["getAgent"]

	props := op.Success.Schema["properties"].(map[string]any)
	parent := props["parent"].(map[string]any)
	if parent["type"] != "object" {
		t.Fatalf("recursive parent = %v, want a plain object schema", parent)
	}
	if _, deeper := parent["properties"]; deeper {
		t.Fatalf("recursive parent was expanded: %v", parent)
	}
}

func TestParseOperations_ReadsBody(t *testing.T) {
	t.Parallel()
	op := parseMini(t)["hireAgent"]

	if op.Body == nil || !op.Body.Required || op.Body.Schema["type"] != "object" {
		t.Fatalf("body = %+v", op.Body)
	}
}

func TestParseOperations_MarksStreamsAndWebSockets(t *testing.T) {
	t.Parallel()
	ops := parseMini(t)

	if !ops["streamAgentOutput"].Stream || ops["streamAgentOutput"].WebSocket {
		t.Errorf("streamAgentOutput: %+v", ops["streamAgentOutput"])
	}
	if !ops["connectPty"].WebSocket || ops["connectPty"].Stream {
		t.Errorf("connectPty: %+v", ops["connectPty"])
	}
	if ops["getAgent"].Stream {
		t.Error("getAgent marked as stream")
	}
}

func TestParseOperations_RejectsBrokenInput(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		doc  string
	}{
		{"not json", `{`},
		{"unresolvable reference", `{"paths":{"/a":{"get":{"operationId":"a","tags":["t"],"parameters":[{"$ref":"#/components/parameters/Nope"}],"responses":{}}}}}`},
		{"missing operation id", `{"paths":{"/a":{"get":{"tags":["t"],"responses":{}}}}}`},
		{"missing tag", `{"paths":{"/a":{"get":{"operationId":"a","responses":{}}}}}`},
		{"tag is not text", `{"paths":{"/a":{"get":{"operationId":"a","tags":[7],"responses":{}}}}}`},
		{"tag is empty", `{"paths":{"/a":{"get":{"operationId":"a","tags":[""],"responses":{}}}}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := domain.ParseOperations([]byte(tt.doc))
			if !errors.Is(err, domain.ErrInvalidSpec) {
				t.Fatalf("error = %v, want ErrInvalidSpec", err)
			}
		})
	}
}

func TestParseOperations_ListsOperationsInStableOrder(t *testing.T) {
	t.Parallel()
	a, _ := domain.ParseOperations([]byte(miniSpec))
	b, _ := domain.ParseOperations([]byte(miniSpec))
	for i := range a {
		if a[i].ID != b[i].ID {
			t.Fatalf("order differs at %d: %s vs %s", i, a[i].ID, b[i].ID)
		}
	}
}
