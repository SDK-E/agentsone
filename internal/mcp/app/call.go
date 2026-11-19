// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/SDK-E/agentsone/internal/mcp/domain"
)

// Errors of a tool call. Argument errors are the domain's own.
var (
	// ErrUnknownTool means no tool has the requested name.
	ErrUnknownTool = errors.New("unknown tool")
	// ErrDispatch means the request could not be handed to the API at all.
	ErrDispatch = errors.New("dispatch failed")
)

// Reply is the API's answer to a dispatched request.
type Reply struct {
	Status int
	Body   []byte
}

// Dispatcher hands a request to the daemon's API in process. The caller is
// the one on ctx (core.CallerFrom): the adapter must use it as is and must
// never derive or widen identity from anything else.
type Dispatcher interface {
	Dispatch(ctx context.Context, r domain.Request) (Reply, error)
}

// Result is what a tool call produced.
type Result struct {
	// Status is the HTTP status the API answered.
	Status int
	// Body is the response body, empty when there was none.
	Body []byte
	// IsError is true for a problem response; Problem holds it.
	IsError bool
	Problem *domain.Problem
}

// CallTool runs one tool call as an API request in the caller's name.
type CallTool struct {
	catalog    *Catalog
	dispatcher Dispatcher
}

// NewCallTool builds the use case.
func NewCallTool(c *Catalog, d Dispatcher) *CallTool {
	return &CallTool{catalog: c, dispatcher: d}
}

// Do builds the request from args, dispatches it with ctx, and reads the
// answer. A problem from the API is a Result with IsError, not an error:
// the agent is meant to read it and decide. Errors are for calls that never
// reached the API.
func (u *CallTool) Do(ctx context.Context, name string, args map[string]any) (Result, error) {
	tool, ok := u.catalog.Tool(name)
	if !ok {
		return Result{}, fmt.Errorf("%w: %q", ErrUnknownTool, name)
	}
	req, err := domain.BuildRequest(tool.Operation, args)
	if err != nil {
		return Result{}, err
	}
	reply, err := u.dispatcher.Dispatch(ctx, req)
	if err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrDispatch, err)
	}
	res := Result{Status: reply.Status, Body: reply.Body}
	if reply.Status >= 400 {
		p := domain.ParseProblem(reply.Status, reply.Body)
		res.IsError, res.Problem = true, &p
	}
	return res, nil
}
