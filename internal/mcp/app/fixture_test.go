// SPDX-License-Identifier: MIT

package app_test

import (
	"context"
	"sync"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/mcp/app"
	"github.com/SDK-E/agentsone/internal/mcp/domain"
)

const miniSpec = `{
  "servers": [{"url": "http://127.0.0.1:7700/v1"}],
  "paths": {
    "/agents/{agent_name}": {
      "get": {
        "operationId": "getAgent", "tags": ["agents"], "summary": "Get an agent",
        "parameters": [{"name": "agent_name", "in": "path", "required": true, "schema": {"type": "string"}}],
        "responses": {"200": {"description": "ok", "content": {"application/json": {"schema": {"type": "object"}}}}}
      }
    },
    "/agents": {
      "post": {
        "operationId": "hireAgent", "tags": ["agents"], "summary": "Hire",
        "requestBody": {"required": true, "content": {"application/json": {"schema": {"type": "object", "required": ["name"], "properties": {"name": {"type": "string"}}}}}},
        "responses": {"201": {"description": "hired", "content": {"application/json": {"schema": {"type": "object"}}}}}
      }
    },
    "/agents/{agent_name}/output": {
      "get": {
        "operationId": "streamAgentOutput", "tags": ["agents"], "summary": "Follow output",
        "x-stream": {"protocol": "sse"},
        "parameters": [{"name": "agent_name", "in": "path", "required": true, "schema": {"type": "string"}}],
        "responses": {"200": {"description": "stream", "content": {"text/event-stream": {"schema": {"type": "object"}}}}}
      }
    }
  }
}`

// fakeDispatcher answers every request with one canned reply and records
// what it saw: the request, and the caller on the context.
type fakeDispatcher struct {
	mu      sync.Mutex
	reply   app.Reply
	err     error
	reqs    []domain.Request
	callers []core.Caller
}

func (f *fakeDispatcher) Dispatch(ctx context.Context, r domain.Request) (app.Reply, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, _ := core.CallerFrom(ctx)
	f.reqs = append(f.reqs, r)
	f.callers = append(f.callers, c)
	return f.reply, f.err
}

func newCatalog(t interface {
	Helper()
	Fatalf(string, ...any)
}) *app.Catalog {
	t.Helper()
	cat, err := app.NewCatalog([]byte(miniSpec))
	if err != nil {
		t.Fatalf("NewCatalog() error = %v", err)
	}
	return cat
}
