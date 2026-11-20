// SPDX-License-Identifier: MIT

package api_test

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	contract "github.com/SDK-E/agentsone/internal/contract/api"
	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/core/coretest"
	"github.com/SDK-E/agentsone/internal/daemon"
)

// Every pattern a daemon module registers beside its generated groups maps
// to a contract operation that names a scope or is public, so no module
// route is reachable without the scope check deciding it.
func TestDaemon_EveryModuleRouteIsAScopedContractOperation(t *testing.T) {
	t.Parallel()
	deps, _ := coretest.NewDeps(t)
	param := regexp.MustCompile(`\{[^}]*\}`)
	n := 0
	mods := daemon.Modules(deps)
	for _, m := range mods {
		m.Provide(deps.Ports)
	}
	for _, m := range mods {
		for _, r := range m.API().Routes {
			n++
			method, path, ok := strings.Cut(r.Pattern, " ")
			if !ok || method == "" || !strings.HasPrefix(path, "/") {
				t.Errorf("module %s route %q names no method; every route must be one contract operation", m.Name(), r.Pattern)
				continue
			}
			route, ok := contract.Match(method, param.ReplaceAllString(path, "p"))
			switch {
			case !ok:
				t.Errorf("module %s route %q is no contract operation", m.Name(), r.Pattern)
			case route.Scope == "" && !route.Public:
				t.Logf("module %s route %q is %s, which takes no token", m.Name(), r.Pattern, route.OperationID)
			}
		}
	}
	t.Logf("%d module routes checked", n)
}

// The real daemon, with every real module, holds a token to its scopes: a
// memory:read token is refused the task list with 403 forbidden before the
// tasks module runs, while tasks:read, and the higher tasks:write and
// tasks:admin, read it.
func TestDaemon_RefusesATokenWithoutTheRouteScope(t *testing.T) {
	t.Parallel()
	deps, _ := coretest.NewDeps(t)
	short, err := os.MkdirTemp("", "a2")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { coretest.Check(t, func() error { return os.RemoveAll(short) }) })
	deps.Config.RuntimeDir, deps.Config.LibDir = short, t.TempDir()

	ctx, cancel := context.WithCancel(t.Context())
	ready := make(chan []net.Addr, 1)
	done := make(chan error, 1)
	go func() { done <- daemon.Serve(ctx, deps, daemon.Modules(deps), func(a []net.Addr) { ready <- a }) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("daemon stopped with %v", err)
		}
	})
	var tcp string
	select {
	case addrs := <-ready:
		tcp = addrs[len(addrs)-1].String()
	case err := <-done:
		t.Fatalf("daemon did not start: %v", err)
	case <-time.After(30 * time.Second):
		t.Fatal("daemon not ready after 30 s")
	}

	scout := core.Caller{Kind: core.CallerAgent, Name: "scout", UID: -1}
	token := func(scope string) string {
		tok, err := deps.Ports.Tokens.Issue(t.Context(), scout, []string{scope}, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		return tok.Value
	}
	call := func(path, tok string) (int, map[string]any) {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+tcp+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+tok)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		var body map[string]any
		if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
			t.Errorf("GET %s: body is not JSON: %v", path, err)
		}
		if err := res.Body.Close(); err != nil {
			t.Errorf("GET %s: close body: %v", path, err)
		}
		return res.StatusCode, body
	}

	memory := token("memory:read")
	if code, body := call("/v1/tasks", memory); code != http.StatusForbidden || body["type"] != "urn:agentsone:problem:forbidden" {
		t.Errorf("memory:read token on listTasks = %d %v, want 403 forbidden", code, body)
	}
	if code, body := call("/v1/tasks", token("tasks:write")); code != http.StatusOK {
		t.Errorf("tasks:write token on listTasks = %d %v, want 200", code, body)
	}
	if code, body := call("/v1/tasks", token("tasks:read")); code != http.StatusOK {
		t.Errorf("tasks:read token on listTasks = %d %v, want 200", code, body)
	}
	if code, body := call("/v1/tasks", token("tasks:admin")); code != http.StatusOK {
		t.Errorf("tasks:admin token on listTasks = %d %v, want 200", code, body)
	}
}
