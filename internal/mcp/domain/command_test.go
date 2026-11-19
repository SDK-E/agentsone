// SPDX-License-Identifier: MIT

package domain_test

import (
	"errors"
	"testing"

	"github.com/SDK-E/agentsone/internal/mcp/domain"
)

func op(id, tag string) domain.Operation {
	return domain.Operation{ID: id, Method: "GET", Path: "/v1/x", Tag: tag}
}

func commandNames(t *testing.T, ops ...domain.Operation) map[string]string {
	t.Helper()
	cmds, err := domain.NewCommands(ops)
	if err != nil {
		t.Fatalf("NewCommands() error = %v", err)
	}
	got := map[string]string{}
	for _, c := range cmds {
		got[c.Operation.ID] = c.Group + " " + c.Name
	}
	return got
}

func TestNewCommands_GroupsByTagAndDropsTheTagNounFromTheName(t *testing.T) {
	t.Parallel()
	got := commandNames(t,
		op("hireAgent", "agents"), op("listAgents", "agents"), op("getAgentOneLine", "agents"),
		op("listChatMessages", "agents"), op("streamEvents", "events"), op("listEmitters", "events"),
		op("getMachineMetrics", "machine"), op("getMe", "session"))

	want := map[string]string{
		"hireAgent":         "agents hire",
		"listAgents":        "agents list",
		"getAgentOneLine":   "agents get-one-line",
		"listChatMessages":  "agents list-chat-messages",
		"streamEvents":      "events stream",
		"listEmitters":      "events list-emitters",
		"getMachineMetrics": "machine get-metrics",
		"getMe":             "session get-me",
	}
	for id, w := range want {
		if got[id] != w {
			t.Errorf("%s -> %q, want %q", id, got[id], w)
		}
	}
}

func TestNewCommands_FallsBackToTheFullNameWhenShortNamesCollide(t *testing.T) {
	t.Parallel()
	// Dropping "Agent" would name both "list".
	got := commandNames(t, op("listAgent", "agents"), op("listAgents", "agents"))

	if got["listAgent"] == got["listAgents"] {
		t.Fatalf("names collide: %v", got)
	}
	if got["listAgent"] != "agents list-agent" || got["listAgents"] != "agents list-agents" {
		t.Fatalf("names = %v, want full kebab names", got)
	}
}

func TestNewCommands_LeavesOutExcludedOperations(t *testing.T) {
	t.Parallel()
	stream := op("streamBus", "bus")
	stream.Stream = true
	ws := op("connectPty", "machine")
	ws.WebSocket = true

	cmds, err := domain.NewCommands([]domain.Operation{op("mcpPost", "mcp"), op("getDoorHealth", "host"), ws, stream})

	if err != nil {
		t.Fatalf("NewCommands() error = %v", err)
	}
	if len(cmds) != 1 || cmds[0].Operation.ID != "streamBus" {
		t.Fatalf("commands = %+v, want only streamBus", cmds)
	}
}

func TestNewCommands_TurnsPathParametersIntoArguments(t *testing.T) {
	t.Parallel()
	o := getOp()

	cmds, err := domain.NewCommands([]domain.Operation{o})

	if err != nil {
		t.Fatalf("NewCommands() error = %v", err)
	}
	c := cmds[0]
	if len(c.Args) != 1 || c.Args[0].Name != "agent_name" {
		t.Fatalf("args = %+v", c.Args)
	}
	if len(c.Flags) != 1 || c.Flags[0].Name != "verbose" || c.Flags[0].Kind != domain.FlagBool || c.Flags[0].From != domain.InQuery {
		t.Fatalf("flags = %+v, want the query flag verbose", c.Flags)
	}
}

func TestNewCommands_TurnsBodyFieldsIntoTypedFlags(t *testing.T) {
	t.Parallel()
	o := hireOp()
	o.Body.Schema = objectSchema(map[string]any{
		"name":      strSchema(),
		"max_turns": domain.Schema{"type": "integer"},
		"ratio":     domain.Schema{"type": "number"},
		"active":    domain.Schema{"type": "boolean"},
		"labels":    domain.Schema{"type": "array", "items": strSchema()},
		"limits":    domain.Schema{"type": "object"},
		"kind":      domain.Schema{"type": "string", "enum": []any{"chat", "waker"}, "description": "How it lives."},
		"nullable":  domain.Schema{"type": []any{"string", "null"}},
		"either":    domain.Schema{"oneOf": []any{strSchema(), domain.Schema{"type": "integer"}}},
	}, "name")

	cmds, err := domain.NewCommands([]domain.Operation{o})

	if err != nil {
		t.Fatalf("NewCommands() error = %v", err)
	}
	byName := map[string]domain.Flag{}
	for _, f := range cmds[0].Flags {
		byName[f.Name] = f
	}
	wantKind := map[string]domain.FlagKind{
		"name": domain.FlagString, "max-turns": domain.FlagInt, "ratio": domain.FlagNumber, "active": domain.FlagBool,
		"labels": domain.FlagList, "limits": domain.FlagJSON, "kind": domain.FlagString, "nullable": domain.FlagString,
		"either": domain.FlagJSON,
	}
	for name, kind := range wantKind {
		if byName[name].Kind != kind {
			t.Errorf("flag %s kind = %q, want %q", name, byName[name].Kind, kind)
		}
	}
	if byName["max-turns"].Key != "max_turns" || byName["max-turns"].From != domain.FromBody {
		t.Errorf("max-turns flag = %+v", byName["max-turns"])
	}
	if !byName["name"].Required || byName["kind"].Required {
		t.Errorf("required: name=%v kind=%v", byName["name"].Required, byName["kind"].Required)
	}
	if byName["kind"].Description != "How it lives." || len(byName["kind"].Enum) != 2 {
		t.Errorf("kind flag = %+v", byName["kind"])
	}
}

func TestNewCommands_RefusesAFlagThatShadowsAGlobalFlag(t *testing.T) {
	t.Parallel()
	o := hireOp()
	o.Body.Schema = objectSchema(map[string]any{"json": strSchema()})

	_, err := domain.NewCommands([]domain.Operation{o})

	if !errors.Is(err, domain.ErrNameCollision) {
		t.Fatalf("NewCommands() error = %v, want ErrNameCollision", err)
	}
}

func TestNewCommands_TakesANonObjectBodyAsTheBodyFlagOnly(t *testing.T) {
	t.Parallel()
	o := hireOp()
	o.Body = &domain.Body{Required: true, Schema: domain.Schema{"type": "array"}}

	cmds, err := domain.NewCommands([]domain.Operation{o})

	if err != nil {
		t.Fatalf("NewCommands() error = %v", err)
	}
	if len(cmds[0].Flags) != 0 || !cmds[0].HasBody || cmds[0].BodyFlat {
		t.Fatalf("command = %+v, want no field flags and a whole-body input", cmds[0])
	}
}

func TestKebab_TurnsSnakeCaseIntoFlagNames(t *testing.T) {
	t.Parallel()
	if got := domain.Kebab("max_turns"); got != "max-turns" {
		t.Fatalf("Kebab = %q", got)
	}
}
