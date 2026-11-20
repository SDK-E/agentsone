// SPDX-License-Identifier: MIT

package app_test

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	httpadapter "github.com/SDK-E/agentsone/internal/api/adapters/http"
	contract "github.com/SDK-E/agentsone/internal/contract/api"
	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/mcp/app"
)

func runnerScopes(t *testing.T) []string {
	t.Helper()
	scopes, err := app.RunnerScopes()
	if err != nil {
		t.Fatal(err)
	}
	return scopes
}

// toolScope is the contract scope of each MCP tool's operation, from the
// catalog the machine MCP serves.
func toolScopes(t *testing.T) map[string]string {
	t.Helper()
	cat, err := app.NewCatalog(contract.Spec)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, tool := range cat.Tools() {
		i := slices.IndexFunc(contract.Routes, func(r contract.Route) bool { return r.OperationID == tool.Operation.ID })
		if i < 0 {
			t.Fatalf("tool %s: operation %s not in the route table", tool.Name, tool.Operation.ID)
		}
		out[tool.Name] = contract.Routes[i].Scope
	}
	return out
}

// No MCP tool a runner gets is refused for want of a scope: the runner keeps
// every tool it had before scopes were checked.
func TestRunnerScopes_CoverEveryToolThatTakesAToken(t *testing.T) {
	t.Parallel()
	held := runnerScopes(t)
	for tool, need := range toolScopes(t) {
		if need != "" && !slices.Contains(held, need) {
			t.Errorf("tool %s needs %s, which the runner token does not hold", tool, need)
		}
	}
	if !slices.Contains(held, "mcp:write") {
		t.Errorf("runner scopes %v miss mcp:write, the machine MCP's own", held)
	}
}

// The runner token holds nothing a tool does not need: no widening beyond
// the tool set.
func TestRunnerScopes_HoldOnlyWhatToolsNeed(t *testing.T) {
	t.Parallel()
	used := map[string]bool{"mcp:write": true}
	for _, need := range toolScopes(t) {
		used[need] = true
	}
	held := runnerScopes(t)
	if !slices.IsSorted(held) || len(slices.Compact(slices.Clone(held))) != len(held) {
		t.Errorf("runner scopes %v are not sorted and unique", held)
	}
	for _, s := range held {
		if !used[s] {
			t.Errorf("runner token holds %s, which no MCP tool needs", s)
		}
	}
}

// A runner token is refused on every operation outside its scopes, and
// served on every one inside them.
func TestRunnerScopes_RefusedOutsideTheToolSet(t *testing.T) {
	t.Parallel()
	runner := core.Caller{Kind: core.CallerAgent, Name: "mira", Scopes: runnerScopes(t), ByToken: true}
	refused := 0
	for _, r := range contract.Routes {
		if r.Public {
			continue
		}
		covered := r.Scope != "" && slices.ContainsFunc(runner.Scopes, func(h string) bool { return contract.Covers(h, r.Scope) })
		ran := false
		h := httpadapter.RequireScopes(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { ran = true }))
		path := strings.NewReplacer("{", "p", "}", "").Replace(r.Path)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequestWithContext(core.WithCaller(t.Context(), runner), r.Method, path, nil))
		switch {
		case covered && !ran:
			t.Errorf("%s (%s) refused a runner token holding it: %d", r.OperationID, r.Scope, rec.Code)
		case !covered && (ran || rec.Code != http.StatusForbidden):
			t.Errorf("%s (%q) outside the runner scopes answered %d, handler ran %v", r.OperationID, r.Scope, rec.Code, ran)
		case !covered:
			refused++
		}
	}
	if refused == 0 {
		t.Error("no operation is outside the runner scopes; the host door and cookie-only operations should be")
	}
}
