// SPDX-License-Identifier: MIT

package httpadapter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	nethttp "net/http"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/mcp/app"
	"github.com/SDK-E/agentsone/internal/mcp/domain"
)

// Path is where the machine MCP is served.
const Path = "/v1/mcp"

// callerExtra is the key under which the session's caller rides in the
// SDK's token info.
const callerExtra = "agentsone.caller"

// sessionMarker stands in for a bearer token. The real identity is the
// Caller the API layer put on the request context; the SDK only needs a
// token to bind each MCP session to one principal.
const sessionMarker = "in-process"

const instructions = "Agents One machine tools. Every tool is one daemon API operation and runs as you: " +
	"the daemon applies your own permissions. A failed call returns the problem type and detail."

// Server is the machine MCP: a Streamable HTTP MCP server whose tools are the
// daemon API.
type Server struct {
	handler nethttp.Handler
}

// NewServer builds the MCP server from the catalog. Each tool call goes through
// call, as the caller bound to the MCP session.
func NewServer(cat *app.Catalog, call *app.CallTool, version string) *Server {
	srv := mcp.NewServer(&mcp.Implementation{Name: "agentsone", Title: "Agents One machine", Version: version},
		&mcp.ServerOptions{Instructions: instructions, PageSize: len(cat.Tools()) + 1})
	for _, tool := range cat.Tools() {
		srv.AddTool(sdkTool(tool), toolHandler(tool, call))
	}
	streamable := mcp.NewStreamableHTTPHandler(func(*nethttp.Request) *mcp.Server { return srv },
		&mcp.StreamableHTTPOptions{JSONResponse: true})
	return &Server{handler: bindCaller(streamable)}
}

// Handler serves /v1/mcp for POST, GET and DELETE.
func (s *Server) Handler() nethttp.Handler { return s.handler }

// Group is the mcp tag as the API layer mounts it. It is built by hand
// because the SDK's handler owns sessions and streams; the generated strict
// server cannot express them.
func (s *Server) Group() core.APIGroup {
	return core.APIGroup{
		Tag: domain.TagMCP,
		Register: func(mux *nethttp.ServeMux, _ core.APIOptions) {
			mux.Handle(Path, s.handler)
		},
	}
}

func sdkTool(t domain.Tool) *mcp.Tool {
	out := &mcp.Tool{
		Name:        t.Name,
		Description: t.Description,
		InputSchema: t.Input,
		Annotations: annotations(t.Operation),
	}
	if t.Output != nil {
		out.OutputSchema = t.Output
	}
	return out
}

// annotations tell clients what a tool does, from the HTTP method alone.
func annotations(op domain.Operation) *mcp.ToolAnnotations {
	open := false
	a := &mcp.ToolAnnotations{Title: op.Summary, OpenWorldHint: &open}
	switch op.Method {
	case nethttp.MethodGet:
		a.ReadOnlyHint = true
	case nethttp.MethodDelete:
		destructive := true
		a.DestructiveHint, a.IdempotentHint = &destructive, true
	case nethttp.MethodPut:
		a.IdempotentHint = true
	}
	return a
}

func toolHandler(tool domain.Tool, call *app.CallTool) mcp.ToolHandler {
	return func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		caller, ok := callerOf(req)
		if !ok {
			return toolError("no caller is bound to this session"), nil
		}
		args, err := decodeArgs(req.Params.Arguments)
		if err != nil {
			return toolError(err.Error()), nil
		}
		res, err := call.Do(core.WithCaller(ctx, caller), tool.Name, args)
		if err != nil {
			return toolError(describeFailure(err)), nil
		}
		return toResult(tool, res), nil
	}
}

func callerOf(req *mcp.CallToolRequest) (core.Caller, bool) {
	if req.Extra == nil || req.Extra.TokenInfo == nil {
		return core.Caller{}, false
	}
	c, ok := req.Extra.TokenInfo.Extra[callerExtra].(core.Caller)
	return c, ok
}

// decodeArgs reads the arguments with numbers kept as written, so a large
// integer is not turned into a float on its way to the API.
func decodeArgs(raw json.RawMessage) (map[string]any, error) {
	if len(bytes.TrimSpace(raw)) == 0 || string(bytes.TrimSpace(raw)) == "null" {
		return map[string]any{}, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var args map[string]any
	if err := dec.Decode(&args); err != nil {
		return nil, fmt.Errorf("arguments must be a JSON object: %w", err)
	}
	return args, nil
}

func describeFailure(err error) string {
	switch {
	case errors.Is(err, domain.ErrMissingArgument), errors.Is(err, domain.ErrUnknownArgument), errors.Is(err, domain.ErrBadArgument):
		return err.Error()
	case errors.Is(err, ErrNotReady), errors.Is(err, ErrNoCaller):
		return "the daemon API is not available to this session: " + err.Error()
	}
	return err.Error()
}

func toolError(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: msg}}}
}

func toResult(tool domain.Tool, res app.Result) *mcp.CallToolResult {
	if res.IsError {
		p := res.Problem
		msg := p.Type + ": " + p.Title
		if p.Detail != "" {
			msg = p.Type + ": " + p.Detail
		}
		return toolError(fmt.Sprintf("%s (status %d)", msg, p.Status))
	}
	body := strings.TrimSpace(string(res.Body))
	if body == "" {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("ok (status %d)", res.Status)}}}
	}
	out := &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: body}}}
	if tool.Output != nil && strings.HasPrefix(body, "{") && json.Valid(res.Body) {
		out.StructuredContent = json.RawMessage(res.Body)
	}
	return out
}

// bindCaller makes every request carry the API layer's caller into the MCP
// session. The SDK refuses a request on a session when its token user differs
// from the one that opened it, so a session cannot change hands.
func bindCaller(next nethttp.Handler) nethttp.Handler {
	guarded := auth.RequireBearerToken(verifyCaller, &auth.RequireBearerTokenOptions{AllowMissingExpiration: true})(next)
	return nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		if _, ok := core.CallerFrom(r.Context()); !ok {
			core.WriteProblem(w, r, core.NewProblem(nethttp.StatusUnauthorized, "unauthorized", "the machine MCP needs a signed-in caller"))
			return
		}
		r = r.Clone(r.Context())
		r.Header.Set("Authorization", "Bearer "+sessionMarker)
		guarded.ServeHTTP(w, r)
	})
}

func verifyCaller(_ context.Context, _ string, req *nethttp.Request) (*auth.TokenInfo, error) {
	c, ok := core.CallerFrom(req.Context())
	if !ok {
		return nil, auth.ErrInvalidToken
	}
	return &auth.TokenInfo{
		UserID: string(c.Kind) + ":" + c.Name,
		Scopes: c.Scopes,
		Extra:  map[string]any{callerExtra: c},
	}, nil
}
