// SPDX-License-Identifier: MIT

package domain

// Schema is a JSON Schema with every $ref resolved in place. A reference
// that would recurse is cut to a plain object schema.
type Schema = map[string]any

// Parameter locations the mapping uses. Header and cookie parameters belong
// to the transport and never become tool inputs or command flags.
const (
	InPath  = "path"
	InQuery = "query"
)

// Param is one path or query parameter of an operation.
type Param struct {
	Name        string
	In          string
	Required    bool
	Description string
	Schema      Schema
}

// Body is the JSON request body of an operation.
type Body struct {
	Required bool
	Schema   Schema
}

// Response is the success response of an operation.
type Response struct {
	Status int
	// Schema is the JSON body schema, or nil when the response has no JSON body.
	Schema Schema
}

// Operation is one API operation, read from the contract. It is the only
// input the mapping rules work on; nothing here is written by hand.
type Operation struct {
	ID          string
	Method      string
	Path        string // with the base path, for example /v1/agents/{agent_name}
	Tag         string
	Summary     string
	Description string
	Params      []Param // path and query only, path parameters first in path order
	Body        *Body
	Success     *Response
	// Stream is true for server-sent event streams (x-stream).
	Stream bool
	// WebSocket is true for operations that upgrade (x-websocket).
	WebSocket bool
}

// PathParams returns the path parameters in path order.
func (o Operation) PathParams() []Param {
	var out []Param
	for _, p := range o.Params {
		if p.In == InPath {
			out = append(out, p)
		}
	}
	return out
}

// QueryParams returns the query parameters in contract order.
func (o Operation) QueryParams() []Param {
	var out []Param
	for _, p := range o.Params {
		if p.In == InQuery {
			out = append(out, p)
		}
	}
	return out
}
