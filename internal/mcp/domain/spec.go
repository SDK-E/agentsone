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
		return nil, fmt.Errorf("%w: %v", ErrInvalidSpec, err)
	}
	r := &resolver{root: root}
	base := basePath(root)

	paths, _ := root["paths"].(map[string]any)
	keys := make([]string, 0, len(paths))
	for p := range paths {
		keys = append(keys, p)
	}
	sort.Strings(keys)

	var ops []Operation
	for _, p := range keys {
		item, _ := paths[p].(map[string]any)
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
	servers, _ := root["servers"].([]any)
	if len(servers) == 0 {
		return ""
	}
	first, _ := servers[0].(map[string]any)
	raw, _ := first["url"].(string)
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return strings.TrimRight(u.Path, "/")
}

type resolver struct{ root map[string]any }

func (r *resolver) operation(method, path string, item, raw map[string]any) (Operation, error) {
	id, _ := raw["operationId"].(string)
	if id == "" {
		return Operation{}, fmt.Errorf("%w: %s %s has no operationId", ErrInvalidSpec, strings.ToUpper(method), path)
	}
	tags, _ := raw["tags"].([]any)
	if len(tags) == 0 {
		return Operation{}, fmt.Errorf("%w: operation %s has no tag", ErrInvalidSpec, id)
	}
	tag, _ := tags[0].(string)
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
				return nil, fmt.Errorf("%w: operation %s: %v", ErrInvalidSpec, id, err)
			}
			m, _ := resolved.(map[string]any)
			in := str(m["in"])
			if in != InPath && in != InQuery {
				continue
			}
			schema, err := r.schema(m["schema"], nil)
			if err != nil {
				return nil, fmt.Errorf("%w: operation %s: %v", ErrInvalidSpec, id, err)
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
		return nil, fmt.Errorf("%w: operation %s: %v", ErrInvalidSpec, id, err)
	}
	m, _ := resolved.(map[string]any)
	content, _ := m["content"].(map[string]any)
	media, ok := content["application/json"].(map[string]any)
	if !ok {
		return nil, nil
	}
	schema, err := r.schema(media["schema"], nil)
	if err != nil {
		return nil, fmt.Errorf("%w: operation %s: %v", ErrInvalidSpec, id, err)
	}
	return &Body{Required: m["required"] == true, Schema: schema}, nil
}

// success picks the lowest 2xx response.
func (r *resolver) success(id string, raw map[string]any) (*Response, error) {
	responses, _ := raw["responses"].(map[string]any)
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
		return nil, fmt.Errorf("%w: operation %s: %v", ErrInvalidSpec, id, err)
	}
	m, _ := resolved.(map[string]any)
	resp := &Response{Status: best}
	content, _ := m["content"].(map[string]any)
	if media, ok := content["application/json"].(map[string]any); ok {
		schema, err := r.schema(media["schema"], nil)
		if err != nil {
			return nil, fmt.Errorf("%w: operation %s: %v", ErrInvalidSpec, id, err)
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
	m, _ := out.(map[string]any)
	return m, nil
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

func str(v any) string {
	s, _ := v.(string)
	return s
}

func anySlice(v any) []any {
	s, _ := v.([]any)
	return s
}
