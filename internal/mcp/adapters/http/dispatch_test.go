// SPDX-License-Identifier: MIT

package httpadapter_test

import (
	"errors"
	"io"
	nethttp "net/http"
	"testing"

	"github.com/SDK-E/agentsone/internal/core"
	mcphttp "github.com/SDK-E/agentsone/internal/mcp/adapters/http"
	"github.com/SDK-E/agentsone/internal/mcp/app"
	"github.com/SDK-E/agentsone/internal/mcp/domain"
)

func TestHandlerDispatcher_RunsTheRequestThroughTheHandlerAsTheCaller(t *testing.T) {
	t.Parallel()
	var seen core.Caller
	var gotMethod, gotURI, gotType, gotBody string
	h := nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		seen, _ = core.CallerFrom(r.Context())
		gotMethod, gotURI, gotType = r.Method, r.URL.RequestURI(), r.Header.Get("Content-Type")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(nethttp.StatusCreated)
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	d := mcphttp.NewHandlerDispatcher(h)
	agent := core.Caller{Kind: core.CallerAgent, Name: "mira", UID: 1001}
	ctx := core.WithCaller(t.Context(), agent)

	reply, err := d.Dispatch(ctx, domain.Request{
		Method: "POST", Path: "/v1/agents", Query: map[string][]string{"dry": {"true"}}, Body: []byte(`{"name":"x"}`),
	})

	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	if reply.Status != 201 || string(reply.Body) != `{"ok":true}` {
		t.Errorf("reply = %+v", reply)
	}
	if seen.Name != "mira" || seen.Kind != core.CallerAgent || seen.IsRoot {
		t.Errorf("handler saw caller %+v, want the same agent", seen)
	}
	if gotMethod != "POST" || gotURI != "/v1/agents?dry=true" || gotType != "application/json" || gotBody != `{"name":"x"}` {
		t.Errorf("request = %s %s %q %q", gotMethod, gotURI, gotType, gotBody)
	}
}

func TestHandlerDispatcher_RefusesACallWithoutACaller(t *testing.T) {
	t.Parallel()
	called := false
	d := mcphttp.NewHandlerDispatcher(nethttp.HandlerFunc(func(nethttp.ResponseWriter, *nethttp.Request) { called = true }))

	_, err := d.Dispatch(t.Context(), domain.Request{Method: "GET", Path: "/v1/agents"})

	if !errors.Is(err, mcphttp.ErrNoCaller) {
		t.Fatalf("Dispatch() error = %v, want ErrNoCaller", err)
	}
	if called {
		t.Fatal("the handler ran for a call without a caller")
	}
}

func TestHandlerDispatcher_FailsUntilTheHandlerIsSet(t *testing.T) {
	t.Parallel()
	d := mcphttp.NewHandlerDispatcher(nil)
	ctx := core.WithCaller(t.Context(), core.Caller{Kind: core.CallerAgent, Name: "mira"})

	_, err := d.Dispatch(ctx, domain.Request{Method: "GET", Path: "/v1/agents"})

	if !errors.Is(err, mcphttp.ErrNotReady) {
		t.Fatalf("Dispatch() error = %v, want ErrNotReady", err)
	}
	var _ app.Dispatcher = d
}

func TestHandlerDispatcher_UsesTheHandlerSetLater(t *testing.T) {
	t.Parallel()
	d := mcphttp.NewHandlerDispatcher(nil)
	d.SetHandler(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, _ *nethttp.Request) { w.WriteHeader(nethttp.StatusNoContent) }))
	ctx := core.WithCaller(t.Context(), core.Caller{Kind: core.CallerHuman, Name: "ana"})

	reply, err := d.Dispatch(ctx, domain.Request{Method: "DELETE", Path: "/v1/session"})

	if err != nil || reply.Status != 204 {
		t.Fatalf("reply = %+v, err = %v", reply, err)
	}
}
