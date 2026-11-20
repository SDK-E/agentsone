// SPDX-License-Identifier: MIT

package domain

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

// ErrInvalidSpec means the API description could not be read.
var ErrInvalidSpec = errors.New("invalid API description")

var httpMethods = []string{"get", "put", "post", "delete", "patch"}

// ParseOperations reads every operation from a bundled OpenAPI 3.1 document.
// The result is sorted by path, then method, so every run sees the same order.
func ParseOperations(doc []byte) ([]Operation, error) {
	var root map[string]any
	if err := json.Unmarshal(doc, &root); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidSpec, err)
	}
	r := &resolver{root: root}
	base := basePath(root)

	paths := object(root["paths"])
	keys := make([]string, 0, len(paths))
	for p := range paths {
		keys = append(keys, p)
	}
	sort.Strings(keys)

	var ops []Operation
	for _, p := range keys {
		item := object(paths[p])
		for _, m := range httpMethods {
			raw, ok := item[m].(map[string]any)
			if !ok {
				continue
			}
			op, err := r.operation(m, base+p, item, raw)
			if err != nil {
				return nil, err
			}
			ops = append(ops, op)
		}
	}
	return ops, nil
}

func basePath(root map[string]any) string {
	servers := anySlice(root["servers"])
	if len(servers) == 0 {
		return ""
	}
	u, err := url.Parse(str(object(servers[0])["url"]))
	if err != nil {
		return ""
	}
	return strings.TrimRight(u.Path, "/")
}

type resolver struct{ root map[string]any }

func (r *resolver) operation(method, path string, item, raw map[string]any) (Operation, error) {
	id := str(raw["operationId"])
	if id == "" {
		return Operation{}, fmt.Errorf("%w: %s %s has no operationId", ErrInvalidSpec, strings.ToUpper(method), path)
	}
	var tag string
	if tags := anySlice(raw["tags"]); len(tags) > 0 {
		tag = str(tags[0])
	}
	if tag == "" {
		return Operation{}, fmt.Errorf("%w: operation %s has no tag", ErrInvalidSpec, id)
	}
	op := Operation{
		ID:          id,
		Method:      strings.ToUpper(method),
		Path:        path,
		Tag:         tag,
		Summary:     str(raw["summary"]),
		Description: strings.TrimSpace(str(raw["description"])),
	}
	_, op.Stream = raw["x-stream"]
	_, op.WebSocket = raw["x-websocket"]

	params, err := r.params(id, item, raw)
	if err != nil {
		return Operation{}, err
	}
	op.Params = orderParams(params, path)

	if body, err := r.body(id, raw); err != nil {
		return Operation{}, err
	} else if body != nil {
		op.Body = body
	}
	resp, err := r.success(id, raw)
	if err != nil {
		return Operation{}, err
	}
	op.Success = resp
	return op, nil
}

// params collects path-level then operation-level parameters. An operation
// parameter replaces a path-level one with the same name and location.
func (r *resolver) params(id string, item, raw map[string]any) ([]Param, error) {
	var out []Param
	seen := map[string]int{}
	for _, list := range [][]any{anySlice(item["parameters"]), anySlice(raw["parameters"])} {
		for _, entry := range list {
			resolved, err := r.deref(entry)
			if err != nil {
				return nil, fmt.Errorf("%w: operation %s: %w", ErrInvalidSpec, id, err)
			}
			m := object(resolved)
			in := str(m["in"])
			if in != InPath && in != InQuery {
				continue
			}
			schema, err := r.schema(m["schema"], nil)
			if err != nil {
				return nil, fmt.Errorf("%w: operation %s: %w", ErrInvalidSpec, id, err)
			}
			p := Param{
				Name:        str(m["name"]),
				In:          in,
				Required:    in == InPath || m["required"] == true,
				Description: strings.TrimSpace(str(m["description"])),
				Schema:      schema,
			}
			key := in + ":" + p.Name
			if at, dup := seen[key]; dup {
				out[at] = p
				continue
			}
			seen[key] = len(out)
			out = append(out, p)
		}
	}
	return out, nil
}

// orderParams puts path parameters first, in the order the path names them.
func orderParams(params []Param, path string) []Param {
	var pathParams, query []Param
	for _, p := range params {
		if p.In == InPath {
			pathParams = append(pathParams, p)
		} else {
			query = append(query, p)
		}
	}
	pos := func(p Param) int { return strings.Index(path, "{"+p.Name+"}") }
	sort.SliceStable(pathParams, func(i, j int) bool { return pos(pathParams[i]) < pos(pathParams[j]) })
	return append(pathParams, query...)
}

func (r *resolver) body(id string, raw map[string]any) (*Body, error) {
	rb, ok := raw["requestBody"]
	if !ok {
		return nil, nil
	}
	resolved, err := r.deref(rb)
	if err != nil {
		return nil, fmt.Errorf("%w: operation %s: %w", ErrInvalidSpec, id, err)
	}
	m := object(resolved)
	media, ok := object(m["content"])["application/json"].(map[string]any)
	if !ok {
		return nil, nil
	}
	schema, err := r.schema(media["schema"], nil)
	if err != nil {
		return nil, fmt.Errorf("%w: operation %s: %w", ErrInvalidSpec, id, err)
	}
	return &Body{Required: m["required"] == true, Schema: schema}, nil
}

// success picks the lowest 2xx response.
func (r *resolver) success(id string, raw map[string]any) (*Response, error) {
	responses := object(raw["responses"])
	best := 0
	for code := range responses {
		n, err := strconv.Atoi(code)
		if err == nil && n >= 200 && n < 300 && (best == 0 || n < best) {
			best = n
		}
	}
	if best == 0 {
		return nil, nil
	}
	resolved, err := r.deref(responses[strconv.Itoa(best)])
	if err != nil {
		return nil, fmt.Errorf("%w: operation %s: %w", ErrInvalidSpec, id, err)
	}
	resp := &Response{Status: best}
	if media, ok := object(object(resolved)["content"])["application/json"].(map[string]any); ok {
		schema, err := r.schema(media["schema"], nil)
		if err != nil {
			return nil, fmt.Errorf("%w: operation %s: %w", ErrInvalidSpec, id, err)
		}
		resp.Schema = schema
	}
	return resp, nil
}

// deref follows a top-level $ref chain.
func (r *resolver) deref(v any) (any, error) {
	for range 16 {
		m, ok := v.(map[string]any)
		if !ok {
			return v, nil
		}
		ref, ok := m["$ref"].(string)
		if !ok {
			return v, nil
		}
		target, err := r.lookup(ref)
		if err != nil {
			return nil, err
		}
		v = target
	}
	return nil, errors.New("reference chain too long")
}

func (r *resolver) lookup(ref string) (any, error) {
	if !strings.HasPrefix(ref, "#/") {
		return nil, fmt.Errorf("reference %q is not local", ref)
	}
	var cur any = r.root
	for _, part := range strings.Split(ref[2:], "/") {
		part = strings.NewReplacer("~1", "/", "~0", "~").Replace(part)
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("reference %q does not resolve", ref)
		}
		cur, ok = m[part]
		if !ok {
			return nil, fmt.Errorf("reference %q does not resolve", ref)
		}
	}
	return cur, nil
}

// schema returns v with every $ref expanded. active holds the references
// being expanded; meeting one again cuts the loop to a plain object schema.
func (r *resolver) schema(v any, active []string) (Schema, error) {
	if v == nil {
		return nil, nil
	}
	out, err := r.expand(v, active)
	if err != nil {
		return nil, err
	}
	return object(out), nil
}

func (r *resolver) expand(v any, active []string) (any, error) {
	switch t := v.(type) {
	case map[string]any:
		if ref, ok := t["$ref"].(string); ok {
			for _, a := range active {
				if a == ref {
					return map[string]any{"type": "object"}, nil
				}
			}
			target, err := r.lookup(ref)
			if err != nil {
				return nil, err
			}
			return r.expand(target, append(append([]string(nil), active...), ref))
		}
		out := make(map[string]any, len(t))
		for k, child := range t {
			e, err := r.expand(child, active)
			if err != nil {
				return nil, err
			}
			out[k] = e
		}
		return out, nil
	case []any:
		out := make([]any, len(t))
		for i, child := range t {
			e, err := r.expand(child, active)
			if err != nil {
				return nil, err
			}
			out[i] = e
		}
		return out, nil
	default:
		return v, nil
	}
}

// as reads a JSON value as T. An absent value or one of another type reads as
// T's zero value: every optional OpenAPI field is read this way, and the
// fields the tools need are checked for emptiness where they are used.
func as[T any](v any) T {
	t, ok := v.(T)
	if !ok {
		var zero T
		return zero
	}
	return t
}

func str(v any) string { return as[string](v) }

func anySlice(v any) []any { return as[[]any](v) }

// object reads a JSON value as an object; anything else is an empty object.
func object(v any) map[string]any { return as[map[string]any](v) }
