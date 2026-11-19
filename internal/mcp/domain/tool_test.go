// SPDX-License-Identifier: MIT

package domain_test

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/SDK-E/agentsone/internal/mcp/domain"
)

func objectSchema(props map[string]any, required ...string) domain.Schema {
	s := domain.Schema{"type": "object", "properties": props}
	if len(required) > 0 {
		req := make([]any, len(required))
		for i, r := range required {
			req[i] = r
		}
		s["required"] = req
	}
	return s
}

func strSchema() domain.Schema { return domain.Schema{"type": "string"} }

func hireOp() domain.Operation {
	return domain.Operation{
		ID: "hireAgent", Method: "POST", Path: "/v1/agents", Tag: "agents",
		Summary: "Hire an agent", Description: "Creates the agent.",
		Body: &domain.Body{Required: true, Schema: objectSchema(map[string]any{
			"name": strSchema(), "kind": domain.Schema{"type": "string", "enum": []any{"chat", "waker"}},
		}, "name")},
		Success: &domain.Response{Status: 201, Schema: objectSchema(map[string]any{"name": strSchema()})},
	}
}

func getOp() domain.Operation {
	return domain.Operation{
		ID: "getAgent", Method: "GET", Path: "/v1/agents/{agent_name}", Tag: "agents", Summary: "Get an agent",
		Params: []domain.Param{
			{Name: "agent_name", In: domain.InPath, Required: true, Description: "Agent name.", Schema: strSchema()},
			{Name: "verbose", In: domain.InQuery, Schema: domain.Schema{"type": "boolean"}},
		},
	}
}

func TestNewTool_NamesAndDescribesFromTheContract(t *testing.T) {
	t.Parallel()

	tool, err := domain.NewTool(hireOp())

	if err != nil {
		t.Fatalf("NewTool() error = %v", err)
	}
	if tool.Name != "hire_agent" {
		t.Errorf("name = %q", tool.Name)
	}
	if !strings.HasPrefix(tool.Description, "Hire an agent") || !strings.Contains(tool.Description, "Creates the agent.") {
		t.Errorf("description = %q, want summary then description", tool.Description)
	}
}

func TestNewTool_FlattensBodyPropertiesIntoInputs(t *testing.T) {
	t.Parallel()

	tool, err := domain.NewTool(hireOp())

	if err != nil {
		t.Fatalf("NewTool() error = %v", err)
	}
	props := tool.Input["properties"].(map[string]any)
	if _, ok := props["name"]; !ok {
		t.Fatalf("input properties = %v, want body field name", props)
	}
	if got := tool.Input["required"]; !slices.Equal(got.([]string), []string{"name"}) {
		t.Errorf("required = %v, want [name]", got)
	}
	if tool.Input["type"] != "object" {
		t.Errorf("input type = %v", tool.Input["type"])
	}
}

func TestNewTool_PutsPathAndQueryParametersInInputs(t *testing.T) {
	t.Parallel()

	tool, err := domain.NewTool(getOp())

	if err != nil {
		t.Fatalf("NewTool() error = %v", err)
	}
	props := tool.Input["properties"].(map[string]any)
	name := props["agent_name"].(map[string]any)
	if name["type"] != "string" || name["description"] != "Agent name." {
		t.Errorf("agent_name = %v", name)
	}
	if _, ok := props["verbose"]; !ok {
		t.Errorf("verbose missing from %v", props)
	}
	if got := tool.Input["required"].([]string); !slices.Equal(got, []string{"agent_name"}) {
		t.Errorf("required = %v, want only the path parameter", got)
	}
}

func TestNewTool_TakesANonObjectBodyAsOneBodyInput(t *testing.T) {
	t.Parallel()
	op := hireOp()
	op.Body = &domain.Body{Required: true, Schema: domain.Schema{"type": "array", "items": strSchema()}}

	tool, err := domain.NewTool(op)

	if err != nil {
		t.Fatalf("NewTool() error = %v", err)
	}
	props := tool.Input["properties"].(map[string]any)
	if _, ok := props["body"]; !ok || len(props) != 1 {
		t.Fatalf("properties = %v, want only body", props)
	}
	if got := tool.Input["required"].([]string); !slices.Equal(got, []string{"body"}) {
		t.Errorf("required = %v, want [body]", got)
	}
}

func TestNewTool_MergesAllOfBodies(t *testing.T) {
	t.Parallel()
	op := hireOp()
	op.Body = &domain.Body{Required: true, Schema: domain.Schema{"allOf": []any{
		objectSchema(map[string]any{"a": strSchema()}, "a"),
		objectSchema(map[string]any{"b": strSchema()}),
	}}}

	tool, err := domain.NewTool(op)

	if err != nil {
		t.Fatalf("NewTool() error = %v", err)
	}
	props := tool.Input["properties"].(map[string]any)
	if _, a := props["a"]; !a {
		t.Fatalf("properties = %v, want a and b", props)
	}
	if _, b := props["b"]; !b {
		t.Fatalf("properties = %v, want a and b", props)
	}
}

func TestNewTool_RefusesAParameterThatSharesANameWithABodyField(t *testing.T) {
	t.Parallel()
	op := hireOp()
	op.Params = []domain.Param{{Name: "name", In: domain.InQuery, Schema: strSchema()}}

	_, err := domain.NewTool(op)

	if !errors.Is(err, domain.ErrNameCollision) {
		t.Fatalf("NewTool() error = %v, want ErrNameCollision", err)
	}
}

func TestNewTool_OffersAnOutputSchemaOnlyForObjectResponses(t *testing.T) {
	t.Parallel()
	listOp := getOp()
	listOp.Success = &domain.Response{Status: 200, Schema: domain.Schema{"type": "array", "items": strSchema()}}
	noBody := getOp()
	noBody.Success = &domain.Response{Status: 204}

	withObject, _ := domain.NewTool(hireOp())
	withArray, _ := domain.NewTool(listOp)
	without, _ := domain.NewTool(noBody)

	if withObject.Output == nil {
		t.Error("object response has no output schema")
	}
	if withArray.Output != nil || without.Output != nil {
		t.Errorf("array output %v, empty output %v, want none", withArray.Output, without.Output)
	}
}

func TestBuildRequest_FillsPathQueryAndBody(t *testing.T) {
	t.Parallel()
	get := getOp()
	hire := hireOp()

	gotGet, err := domain.BuildRequest(get, map[string]any{"agent_name": "mira", "verbose": true})
	if err != nil {
		t.Fatalf("BuildRequest(get) error = %v", err)
	}
	gotHire, err := domain.BuildRequest(hire, map[string]any{"name": "mira", "kind": "chat"})
	if err != nil {
		t.Fatalf("BuildRequest(hire) error = %v", err)
	}

	if gotGet.Method != "GET" || gotGet.Path != "/v1/agents/mira" || gotGet.Query.Get("verbose") != "true" || gotGet.Body != nil {
		t.Errorf("get request = %+v", gotGet)
	}
	var body map[string]any
	if err := json.Unmarshal(gotHire.Body, &body); err != nil || body["name"] != "mira" || body["kind"] != "chat" {
		t.Errorf("hire body = %s (%v)", gotHire.Body, err)
	}
}

func TestBuildRequest_EscapesPathValues(t *testing.T) {
	t.Parallel()

	got, err := domain.BuildRequest(getOp(), map[string]any{"agent_name": "a/b c"})

	if err != nil {
		t.Fatalf("BuildRequest() error = %v", err)
	}
	if got.Path != "/v1/agents/a%2Fb%20c" {
		t.Fatalf("path = %q, want an escaped segment", got.Path)
	}
}

func TestBuildRequest_RepeatsArrayQueryValues(t *testing.T) {
	t.Parallel()
	op := getOp()
	op.Params = append(op.Params, domain.Param{Name: "tag", In: domain.InQuery, Schema: domain.Schema{"type": "array"}})

	got, err := domain.BuildRequest(op, map[string]any{"agent_name": "x", "tag": []any{"a", "b"}})

	if err != nil {
		t.Fatalf("BuildRequest() error = %v", err)
	}
	if !slices.Equal(got.Query["tag"], []string{"a", "b"}) {
		t.Fatalf("tag = %v", got.Query["tag"])
	}
}

func TestBuildRequest_RejectsBadArguments(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		op   domain.Operation
		args map[string]any
		want error
	}{
		{"missing path parameter", getOp(), map[string]any{}, domain.ErrMissingArgument},
		{"empty path parameter", getOp(), map[string]any{"agent_name": ""}, domain.ErrMissingArgument},
		{"unknown argument without a body", getOp(), map[string]any{"agent_name": "x", "nope": 1}, domain.ErrUnknownArgument},
		{"object where a path value is expected", getOp(), map[string]any{"agent_name": map[string]any{}}, domain.ErrBadArgument},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := domain.BuildRequest(tt.op, tt.args)
			if !errors.Is(err, tt.want) {
				t.Fatalf("BuildRequest() error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestBuildRequest_SendsUnknownArgumentsInTheBodyForServerValidation(t *testing.T) {
	t.Parallel()

	got, err := domain.BuildRequest(hireOp(), map[string]any{"name": "x", "surprise": 1})

	if err != nil {
		t.Fatalf("BuildRequest() error = %v", err)
	}
	if !strings.Contains(string(got.Body), "surprise") {
		t.Fatalf("body = %s, want the extra field passed through", got.Body)
	}
}

func TestBuildRequest_SendsABodyInputAsTheWholeBody(t *testing.T) {
	t.Parallel()
	op := hireOp()
	op.Body = &domain.Body{Required: true, Schema: domain.Schema{"type": "array"}}

	got, err := domain.BuildRequest(op, map[string]any{"body": []any{"a"}})

	if err != nil {
		t.Fatalf("BuildRequest() error = %v", err)
	}
	if string(got.Body) != `["a"]` {
		t.Fatalf("body = %s", got.Body)
	}
}

func TestBuildRequest_SendsAnEmptyObjectForARequiredEmptyBody(t *testing.T) {
	t.Parallel()
	op := hireOp()

	got, err := domain.BuildRequest(op, map[string]any{})

	if err != nil {
		t.Fatalf("BuildRequest() error = %v", err)
	}
	if string(got.Body) != `{}` {
		t.Fatalf("body = %s, want {}", got.Body)
	}
}
