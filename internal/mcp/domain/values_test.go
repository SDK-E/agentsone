// SPDX-License-Identifier: MIT

package domain_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/SDK-E/agentsone/internal/mcp/domain"
)

func TestBuildRequest_WritesEachScalarKindIntoPathAndQuery(t *testing.T) {
	t.Parallel()
	op := domain.Operation{
		ID: "listThings", Method: "GET", Path: "/v1/things/{id}", Tag: "things",
		Params: []domain.Param{
			{Name: "id", In: domain.InPath, Required: true, Schema: domain.Schema{"type": "integer"}},
			{Name: "a", In: domain.InQuery, Schema: domain.Schema{"type": "boolean"}},
			{Name: "b", In: domain.InQuery, Schema: domain.Schema{"type": "boolean"}},
			{Name: "c", In: domain.InQuery, Schema: domain.Schema{"type": "number"}},
			{Name: "d", In: domain.InQuery, Schema: domain.Schema{"type": "integer"}},
			{Name: "e", In: domain.InQuery, Schema: domain.Schema{"type": "integer"}},
			{Name: "f", In: domain.InQuery, Schema: domain.Schema{"type": "number"}},
		},
	}

	got, err := domain.BuildRequest(op, map[string]any{
		"id": json.Number("7"), "a": true, "b": false, "c": 1.5, "d": 3, "e": int64(4), "f": json.Number("2.5"),
	})

	if err != nil {
		t.Fatalf("BuildRequest() error = %v", err)
	}
	if got.Path != "/v1/things/7" {
		t.Errorf("path = %q", got.Path)
	}
	if q := got.Query.Encode(); q != "a=true&b=false&c=1.5&d=3&e=4&f=2.5" {
		t.Errorf("query = %q", q)
	}
}

func TestBuildRequest_RefusesAnObjectInAQueryValue(t *testing.T) {
	t.Parallel()
	op := domain.Operation{
		ID: "listThings", Method: "GET", Path: "/v1/things", Tag: "things",
		Params: []domain.Param{{Name: "a", In: domain.InQuery, Schema: domain.Schema{"type": "string"}}},
	}

	_, err := domain.BuildRequest(op, map[string]any{"a": map[string]any{"x": 1}})

	if !errors.Is(err, domain.ErrBadArgument) {
		t.Fatalf("err = %v, want ErrBadArgument", err)
	}
}

func TestNewTool_DescribesFromEitherTextAlone(t *testing.T) {
	t.Parallel()
	onlyDescription, err := domain.NewTool(domain.Operation{ID: "doIt", Method: "POST", Path: "/v1/x", Tag: "x", Description: "Does it."})
	if err != nil {
		t.Fatal(err)
	}
	onlySummary, err := domain.NewTool(domain.Operation{ID: "doIt", Method: "POST", Path: "/v1/x", Tag: "x", Summary: "Do it"})
	if err != nil {
		t.Fatal(err)
	}

	if onlyDescription.Description != "Does it." || onlySummary.Description != "Do it" {
		t.Fatalf("descriptions = %q, %q", onlyDescription.Description, onlySummary.Description)
	}
}

func TestNewCommands_NamesDropTheGroupNounInEitherForm(t *testing.T) {
	t.Parallel()
	ops := []domain.Operation{
		{ID: "listCategories", Method: "GET", Path: "/v1/categories", Tag: "categories"},
		{ID: "getCategory", Method: "GET", Path: "/v1/categories/{n}", Tag: "categories",
			Params: []domain.Param{{Name: "n", In: domain.InPath, Required: true, Schema: domain.Schema{"type": "string"}}}},
		{ID: "getBus", Method: "GET", Path: "/v1/bus/{n}", Tag: "bus",
			Params: []domain.Param{{Name: "n", In: domain.InPath, Required: true, Schema: domain.Schema{"type": "string"}}}},
	}

	cmds, err := domain.NewCommands(ops)

	if err != nil {
		t.Fatalf("NewCommands() error = %v", err)
	}
	var names []string
	for _, c := range cmds {
		names = append(names, c.Group+" "+c.Name)
	}
	want := []string{"bus get", "categories get", "categories list"}
	if len(names) != 3 || names[0] != want[0] || names[1] != want[1] || names[2] != want[2] {
		t.Fatalf("commands = %v, want %v", names, want)
	}
}

func TestNewCommands_TypesTheFlagsFromTheirSchemas(t *testing.T) {
	t.Parallel()
	op := domain.Operation{
		ID: "setThing", Method: "PUT", Path: "/v1/things", Tag: "things",
		Params: []domain.Param{
			{Name: "nullable", In: domain.InQuery, Schema: domain.Schema{"type": []any{"null", "integer"}}},
			{Name: "either", In: domain.InQuery, Schema: domain.Schema{"anyOf": []any{}}},
			{Name: "nums", In: domain.InQuery, Schema: domain.Schema{"type": "array", "items": domain.Schema{"type": "integer"}}},
			{Name: "all", In: domain.InQuery, Schema: domain.Schema{"allOf": []any{}}},
		},
	}

	cmds, err := domain.NewCommands([]domain.Operation{op})

	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]domain.FlagKind{}
	for _, f := range cmds[0].Flags {
		kinds[f.Name] = f.Kind
	}
	if kinds["nullable"] != domain.FlagInt || kinds["either"] != domain.FlagJSON || kinds["nums"] != domain.FlagJSON || kinds["all"] != domain.FlagJSON {
		t.Fatalf("kinds = %v", kinds)
	}
}
