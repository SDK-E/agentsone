// SPDX-License-Identifier: MIT

package app_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/agents/supervisor/app"
	"github.com/SDK-E/agentsone/internal/core"
	ledger "github.com/SDK-E/agentsone/internal/edge/budget/domain"
	gatewayhttp "github.com/SDK-E/agentsone/internal/edge/gateway/adapters/http"
	gatewayapp "github.com/SDK-E/agentsone/internal/edge/gateway/app"
	routing "github.com/SDK-E/agentsone/internal/edge/gateway/domain"
	"github.com/SDK-E/agentsone/internal/gatewaytoken"
	mcphttp "github.com/SDK-E/agentsone/internal/mcp/adapters/http"
	mcpapp "github.com/SDK-E/agentsone/internal/mcp/app"
	"github.com/SDK-E/agentsone/runners/openai"
	"github.com/SDK-E/agentsone/runners/sdk"
)

// The welcome hands a runner two addresses. These tests walk the address the
// supervisor really hands out into the real server it names, with the real
// runner client on the other end, so a path that drifts from what the server
// serves fails here and not in a running machine.

// redirect points the supervisor's address at a test server and keeps its path.
func redirect(t *testing.T, handed string, srv *httptest.Server) string {
	t.Helper()
	u, err := url.Parse(handed)
	if err != nil {
		t.Fatalf("parse %q: %v", handed, err)
	}
	s, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parse %q: %v", srv.URL, err)
	}
	u.Scheme, u.Host = s.Scheme, s.Host
	return u.String()
}

type streamRouter struct{}

func (streamRouter) Call(context.Context, gatewayapp.CallRequest) (*gatewayapp.CallResult, error) {
	body := `data: {"model":"autoagents/one","choices":[{"delta":{"content":"hello"}}]}` + "\n\n" + "data: [DONE]\n\n"
	return &gatewayapp.CallResult{
		Model:  routing.Model{Provider: "fake", ID: "fake-model"},
		Status: http.StatusOK,
		Header: http.Header{"Content-Type": {"text/event-stream"}},
		Body:   readCloser{strings.NewReader(body)},
	}, nil
}

func (streamRouter) CountTokens(context.Context, gatewayapp.CountRequest) (*gatewayapp.CallResult, error) {
	return nil, nil
}

func (streamRouter) Budget(ledger.Caller) gatewayapp.BudgetView { return gatewayapp.BudgetView{} }

type readCloser struct{ *strings.Reader }

func (readCloser) Close() error { return nil }

type fixedClock struct{ at time.Time }

func (c fixedClock) Now() time.Time { return c.at }

func TestWelcomeGatewayAddress_AnswersTheRunnersFirstModelCall(t *testing.T) {
	t.Parallel()
	key := []byte("wire-test-gateway-key")
	now := time.Date(2026, 11, 20, 3, 0, 0, 0, time.UTC)
	srv := httptest.NewServer(gatewayhttp.New(gatewayhttp.Options{
		Router: streamRouter{}, Key: func() []byte { return key }, Clock: fixedClock{now},
		Frozen: func(context.Context) (bool, error) { return false, nil },
	}))
	t.Cleanup(srv.Close)
	tok, err := gatewaytoken.Sign(key, gatewaytoken.Claims{
		Agent: "founder", Kind: gatewaytoken.KindAlwaysOn, Chart: "founder", Audience: gatewaytoken.AudienceGateway,
		IssuedAt: now.Unix(), ExpiresAt: now.Add(10 * time.Minute).Unix(),
	})
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	gw := &openai.Gateway{
		BaseURL: redirect(t, app.DefaultConfig().GatewayURL, srv),
		Token:   func() sdk.Secret { return sdk.Secret(tok) },
		Model:   "autoagents/one", HTTP: srv.Client(),
	}

	got, err := gw.Complete(t.Context(), openai.ChatRequest{}, func(string) error { return nil })

	if err != nil || got.Content != "hello" {
		t.Fatalf("Complete() = %q, %v; want the model's answer from the address the welcome hands out (%s)", got.Content, err, gw.BaseURL)
	}
}

const wireSpec = `{
  "servers": [{"url": "http://127.0.0.1:7700/v1"}],
  "paths": {"/agents/{agent_name}": {"get": {
    "operationId": "getAgent", "tags": ["agents"], "summary": "Get an agent",
    "parameters": [{"name": "agent_name", "in": "path", "required": true, "schema": {"type": "string"}}],
    "responses": {"200": {"description": "ok"}}}}}
}`

func TestWelcomeMCPAddress_ListsTheRunnersTools(t *testing.T) {
	t.Parallel()
	cat, err := mcpapp.NewCatalog([]byte(wireSpec))
	if err != nil {
		t.Fatalf("NewCatalog: %v", err)
	}
	server := mcphttp.NewServer(cat, mcpapp.NewCallTool(cat, mcphttp.NewHandlerDispatcher(http.NewServeMux())), "test")
	mux := http.NewServeMux()
	server.Group().Register(mux, core.APIOptions{})
	asFounder := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c := core.Caller{Kind: core.CallerAgent, Name: "founder", UID: 1001}
		mux.ServeHTTP(w, r.WithContext(core.WithCaller(r.Context(), c)))
	})
	srv := httptest.NewServer(asFounder)
	t.Cleanup(srv.Close)

	box, err := openai.DialMCP(t.Context(), sdk.MCP{
		Transport: "streamable-http", URL: redirect(t, app.DefaultConfig().MCPURL, srv),
	}, func() sdk.Secret { return "mcp-token" }, srv.Client())
	if err != nil {
		t.Fatalf("DialMCP at the address the welcome hands out: %v", err)
	}
	t.Cleanup(func() { _ = box.Close() })
	defs, err := box.Tools(t.Context())

	if err != nil || len(defs) != 1 || defs[0].Function.Name != "get_agent" {
		t.Fatalf("Tools() = %+v, %v; want the machine's tools", defs, err)
	}
}
