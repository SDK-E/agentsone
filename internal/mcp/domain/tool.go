// SPDX-License-Identifier: MIT

package domain

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
)

// Errors the mapping can return. Callers test them with errors.Is.
var (
	// ErrNameCollision means two inputs of one operation share a name, so the
	// flat input schema would lose one of them.
	ErrNameCollision = errors.New("input name collision")
	// ErrMissingArgument means a required input was not given.
	ErrMissingArgument = errors.New("missing argument")
	// ErrUnknownArgument means an input matches no parameter and the operation has no body.
	ErrUnknownArgument = errors.New("unknown argument")
	// ErrBadArgument means an input has a type the operation cannot use.
	ErrBadArgument = errors.New("bad argument")
)

// BodyInput is the input name that carries a request body that is not a
// plain object with properties.
const BodyInput = "body"

// Tool is one MCP tool, derived from one operation.
type Tool struct {
	Name        string
	Description string
	// Input is the JSON Schema of the tool arguments: path and query
	// parameters, and the body fields, side by side.
	Input Schema
	// Output is the JSON Schema of the response body, only when it is an object.
	Output    Schema
	Operation Operation
}

// NewTool derives the tool for op. Callers check ExcludeFromTools first.
func NewTool(op Operation) (Tool, error) {
	in, err := op.InputSchema()
	if err != nil {
		return Tool{}, err
	}
	t := Tool{
		Name:        ToolName(op.ID),
		Description: describe(op),
		Input:       in,
		Operation:   op,
	}
	if op.Success != nil && op.Success.Schema != nil && op.Success.Schema["type"] == "object" {
		t.Output = op.Success.Schema
	}
	return t, nil
}

func describe(op Operation) string {
	switch {
	case op.Summary == "":
		return op.Description
	case op.Description == "":
		return op.Summary
	}
	return op.Summary + "\n\n" + op.Description
}

// bodyFields describes how the request body maps to inputs. When flat is
// true the body is an object whose properties become inputs; otherwise the
// whole body is the single input named BodyInput.
type bodyFields struct {
	flat       bool
	properties map[string]any
	required   []string
}

func (o Operation) bodyFields() bodyFields {
	if o.Body == nil {
		return bodyFields{}
	}
	props, required, ok := objectProperties(o.Body.Schema)
	if !ok {
		return bodyFields{}
	}
	return bodyFields{flat: true, properties: props, required: required}
}

// objectProperties collects the properties of an object schema, merging allOf
// members. It reports false for anything else (arrays, oneOf, free-form maps).
func objectProperties(s Schema) (map[string]any, []string, bool) {
	props := map[string]any{}
	var required []string
	found := false
	if p, ok := s["properties"].(map[string]any); ok && s["type"] == "object" {
		found = true
		for k, v := range p {
			props[k] = v
		}
		required = append(required, stringList(s["required"])...)
	}
	if members, ok := s["allOf"].([]any); ok {
		for _, m := range members {
			ms, _ := m.(map[string]any)
			p, r, ok := objectProperties(ms)
			if !ok {
				return nil, nil, false
			}
			found = true
			for k, v := range p {
				props[k] = v
			}
			required = append(required, r...)
		}
	}
	if !found {
		return nil, nil, false
	}
	if _, composed := s["oneOf"]; composed {
		return nil, nil, false
	}
	if _, composed := s["anyOf"]; composed {
		return nil, nil, false
	}
	return props, required, true
}

// InputSchema assembles the flat JSON Schema of the inputs: path parameters,
// query parameters, then body fields. Names must be unique across all three.
func (o Operation) InputSchema() (Schema, error) {
	props := map[string]any{}
	var required []string
	add := func(name string, schema any, req bool) error {
		if _, dup := props[name]; dup {
			return fmt.Errorf("%w: %s has more than one input named %q", ErrNameCollision, o.ID, name)
		}
		props[name] = schema
		if req {
			required = append(required, name)
		}
		return nil
	}
	for _, p := range o.Params {
		if err := add(p.Name, paramSchema(p), p.Required); err != nil {
			return nil, err
		}
	}
	if o.Body != nil {
		bf := o.bodyFields()
		if bf.flat {
			names := make([]string, 0, len(bf.properties))
			for k := range bf.properties {
				names = append(names, k)
			}
			sort.Strings(names)
			reqSet := map[string]bool{}
			for _, r := range bf.required {
				reqSet[r] = true
			}
			for _, k := range names {
				if err := add(k, bf.properties[k], o.Body.Required && reqSet[k]); err != nil {
					return nil, err
				}
			}
		} else if err := add(BodyInput, bodySchema(o.Body), o.Body.Required); err != nil {
			return nil, err
		}
	}
	out := Schema{"type": "object", "properties": props}
	if len(required) > 0 {
		out["required"] = required
	}
	return out, nil
}

func paramSchema(p Param) Schema {
	out := Schema{}
	for k, v := range p.Schema {
		out[k] = v
	}
	if p.Description != "" {
		if _, has := out["description"]; !has {
			out["description"] = p.Description
		}
	}
	return out
}

func bodySchema(b *Body) Schema {
	out := Schema{}
	for k, v := range b.Schema {
		out[k] = v
	}
	if _, has := out["description"]; !has {
		out["description"] = "The request body."
	}
	return out
}

// Request is an API request built from tool or command inputs.
type Request struct {
	Method string
	Path   string // escaped, with the base path
	Query  url.Values
	Body   []byte // JSON, nil when the operation sends none
}

// BuildRequest turns inputs into the API request for op. Path values are
// escaped. Inputs that match no parameter go in the body, where the daemon
// validates them; an operation without a body refuses them.
func BuildRequest(op Operation, args map[string]any) (Request, error) {
	req := Request{Method: op.Method, Query: url.Values{}}
	used := map[string]bool{}

	path := op.Path
	for _, p := range op.PathParams() {
		used[p.Name] = true
		v, ok := args[p.Name]
		text, err := scalarText(p.Name, v)
		if err != nil {
			return Request{}, err
		}
		if !ok || text == "" {
			return Request{}, fmt.Errorf("%w: %s", ErrMissingArgument, p.Name)
		}
		path = strings.Replace(path, "{"+p.Name+"}", url.PathEscape(text), 1)
	}
	req.Path = path

	for _, p := range op.QueryParams() {
		used[p.Name] = true
		v, ok := args[p.Name]
		if !ok || v == nil {
			if p.Required {
				return Request{}, fmt.Errorf("%w: %s", ErrMissingArgument, p.Name)
			}
			continue
		}
		if list, isList := v.([]any); isList {
			for _, item := range list {
				text, err := scalarText(p.Name, item)
				if err != nil {
					return Request{}, err
				}
				req.Query.Add(p.Name, text)
			}
			continue
		}
		text, err := scalarText(p.Name, v)
		if err != nil {
			return Request{}, err
		}
		req.Query.Set(p.Name, text)
	}

	rest := map[string]any{}
	for k, v := range args {
		if !used[k] {
			rest[k] = v
		}
	}
	return finishBody(op, req, rest)
}

func finishBody(op Operation, req Request, rest map[string]any) (Request, error) {
	if op.Body == nil {
		for k := range rest {
			return Request{}, fmt.Errorf("%w: %s", ErrUnknownArgument, k)
		}
		return req, nil
	}
	var payload any
	if op.bodyFields().flat {
		if len(rest) == 0 && !op.Body.Required {
			return req, nil
		}
		payload = rest
	} else {
		v, ok := rest[BodyInput]
		if !ok {
			if op.Body.Required {
				return Request{}, fmt.Errorf("%w: %s", ErrMissingArgument, BodyInput)
			}
			return req, nil
		}
		delete(rest, BodyInput)
		for k := range rest {
			return Request{}, fmt.Errorf("%w: %s", ErrUnknownArgument, k)
		}
		payload = v
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return Request{}, fmt.Errorf("%w: body: %v", ErrBadArgument, err)
	}
	req.Body = b
	return req, nil
}

// scalarText renders a string, number or bool for a path or query value.
func scalarText(name string, v any) (string, error) {
	switch t := v.(type) {
	case nil:
		return "", nil
	case string:
		return t, nil
	case bool:
		if t {
			return "true", nil
		}
		return "false", nil
	case json.Number:
		return t.String(), nil
	case float64:
		return json.Number(fmt.Sprintf("%v", t)).String(), nil
	case int:
		return fmt.Sprint(t), nil
	case int64:
		return fmt.Sprint(t), nil
	}
	return "", fmt.Errorf("%w: %s must be a string, number or boolean", ErrBadArgument, name)
}

func stringList(v any) []string {
	var out []string
	switch t := v.(type) {
	case []string:
		return t
	case []any:
		for _, e := range t {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}
