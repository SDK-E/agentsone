// SPDX-License-Identifier: MIT

package httpadapter_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strings"
	"testing"

	httpadapter "github.com/SDK-E/agentsone/internal/api/adapters/http"
	contract "github.com/SDK-E/agentsone/internal/contract/api"
	"github.com/SDK-E/agentsone/internal/core"
)

// declared is what the bundled contract itself says about one operation's
// security, read straight from contract/dist/openapi.json so the route table
// is checked against the source, not against itself.
type declared struct {
	public bool     // the security list is empty: anyone may call
	bearer bool     // bearerAuth is listed
	scopes []string // the scopes bearerAuth names
}

func declaredSecurity(t *testing.T) map[string]declared {
	t.Helper()
	var doc struct {
		Security []map[string][]string `json:"security"`
		Paths    map[string]map[string]json.RawMessage
	}
	if err := json.Unmarshal(contract.Spec, &doc); err != nil {
		t.Fatal(err)
	}
	out := map[string]declared{}
	for _, item := range doc.Paths {
		for method, raw := range item {
			if !slices.Contains([]string{"get", "put", "post", "delete", "patch", "head", "options", "trace"}, method) {
				continue
			}
			var op struct {
				OperationID string                 `json:"operationId"`
				Security    *[]map[string][]string `json:"security"`
			}
			if err := json.Unmarshal(raw, &op); err != nil {
				t.Fatal(err)
			}
			security := doc.Security
			if op.Security != nil {
				security = *op.Security
			}
			d := declared{public: len(security) == 0}
			for _, req := range security {
				if s, ok := req["bearerAuth"]; ok {
					d.bearer = true
					d.scopes = append(d.scopes, s...)
				}
			}
			out[op.OperationID] = d
		}
	}
	return out
}

var scopeForm = regexp.MustCompile(`^([a-z]+(?:\.[a-z-]+)*):(read|write|admin)$`)

// concrete fills every path parameter, so the route matches itself.
func concrete(r contract.Route) string {
	return strings.NewReplacer("{", "p", "}", "").Replace(r.Path)
}

// othersAdmin is the admin scope of every group but group: a wide token that
// still lacks the one group the operation needs.
func othersAdmin(group string) []string {
	var out []string
	for _, s := range contract.AllScopes() {
		if !strings.HasPrefix(s, group+":") {
			out = append(out, s)
		}
	}
	return out
}

func serve(t *testing.T, r contract.Route, c core.Caller) (int, bool) {
	t.Helper()
	ran := false
	h := httpadapter.RequireScopes(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		ran = true
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequestWithContext(core.WithCaller(t.Context(), c), r.Method, concrete(r), nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if !ran && rec.Code == http.StatusForbidden {
		var p map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil || p["type"] != "urn:agentsone:problem:forbidden" {
			t.Errorf("%s: 403 body %s is not the forbidden problem", r.OperationID, rec.Body)
		}
	}
	return rec.Code, ran
}

// Every operation in the contract states what a token needs, and the
// middleware holds tokens to it: a route that takes a token without naming
// its scope fails here, so it cannot ship unchecked.
func TestRequireScopes_EveryContractRoute(t *testing.T) {
	t.Parallel()
	decl := declaredSecurity(t)
	if len(contract.Routes) != len(decl) {
		t.Fatalf("route table has %d operations, the contract %d", len(contract.Routes), len(decl))
	}
	public, noToken, scoped := 0, 0, 0
	for _, r := range contract.Routes {
		d, ok := decl[r.OperationID]
		if !ok {
			t.Errorf("%s is in the route table but not in the contract", r.OperationID)
			continue
		}
		if d.public != r.Public {
			t.Errorf("%s: route table Public = %v, contract says %v", r.OperationID, r.Public, d.public)
		}
		switch {
		case d.public:
			public++
			if code, ran := serve(t, r, core.Caller{Kind: core.CallerAgent, Name: "scout", ByToken: true}); !ran {
				t.Errorf("%s is public but a token without scopes got %d", r.OperationID, code)
			}
		case !d.bearer:
			noToken++
			if r.Scope != "" {
				t.Errorf("%s takes no token but the route table names scope %q", r.OperationID, r.Scope)
			}
			wide := core.Caller{Kind: core.CallerAgent, Name: "scout", Scopes: contract.AllScopes(), ByToken: true}
			if code, ran := serve(t, r, wide); ran || code != http.StatusForbidden {
				t.Errorf("%s takes no token, but a token holding every scope got %d (handler ran %v)", r.OperationID, code, ran)
			}
		default:
			scoped++
			if len(d.scopes) != 1 {
				t.Errorf("%s: bearerAuth names %d scopes, want exactly one", r.OperationID, len(d.scopes))
				continue
			}
			need := d.scopes[0]
			m := scopeForm.FindStringSubmatch(need)
			if m == nil || m[1] != r.Tag {
				t.Errorf("%s: scope %q is not <%s>:<read|write|admin>", r.OperationID, need, r.Tag)
				continue
			}
			if r.Scope != need {
				t.Errorf("%s: route table scope %q, contract %q", r.OperationID, r.Scope, need)
			}
			without := core.Caller{Kind: core.CallerAgent, Name: "scout", Scopes: othersAdmin(r.Tag), ByToken: true}
			if code, ran := serve(t, r, without); ran || code != http.StatusForbidden {
				t.Errorf("%s: token without %s got %d (handler ran %v), want 403", r.OperationID, need, code, ran)
			}
			exact := core.Caller{Kind: core.CallerAgent, Name: "scout", Scopes: []string{need}, ByToken: true}
			if code, ran := serve(t, r, exact); !ran {
				t.Errorf("%s: token holding %s got %d", r.OperationID, need, code)
			}
			admin := core.Caller{Kind: core.CallerPlugin, Name: "plg_x", Scopes: []string{r.Tag + ":admin"}, ByToken: true}
			if code, ran := serve(t, r, admin); !ran {
				t.Errorf("%s: token holding %s:admin got %d", r.OperationID, r.Tag, code)
			}
		}
		// Sessions and socket peers carry no scopes; kind and chart rules decide.
		for _, c := range []core.Caller{ada, {Kind: core.CallerAgent, Name: "scout", UID: 1001}} {
			if code, ran := serve(t, r, c); !ran {
				t.Errorf("%s: %s caller without a token got %d", r.OperationID, c.Kind, code)
			}
		}
	}
	t.Logf("%d operations: %d public, %d take no token, %d scoped", len(contract.Routes), public, noToken, scoped)
	if public != 3 || scoped == 0 {
		t.Errorf("public = %d, scoped = %d; a change to who may call without a token belongs in a contract change", public, scoped)
	}
}

func TestRequireScopes_LevelsOnlyCoverTheirOwnGroup(t *testing.T) {
	t.Parallel()
	tests := []struct {
		held, need string
		want       bool
	}{
		{"tasks:read", "tasks:read", true},
		{"tasks:write", "tasks:read", true},
		{"tasks:admin", "tasks:write", true},
		{"tasks:read", "tasks:write", false},
		{"tasks:write", "tasks:admin", false},
		{"memory:admin", "tasks:read", false},
		{"tasks:own", "tasks:read", false},
		{"tasks", "tasks:read", false},
		{"task:admin", "tasks:read", false},
	}
	for _, tt := range tests {
		if got := contract.Covers(tt.held, tt.need); got != tt.want {
			t.Errorf("Covers(%q, %q) = %v, want %v", tt.held, tt.need, got, tt.want)
		}
	}
}

// Paths outside the contract are not the middleware's to judge: the
// interface is served to anyone and unknown API paths answer 404 later.
func TestRequireScopes_LeavesPathsOutsideTheContract(t *testing.T) {
	t.Parallel()
	for _, path := range []string{"/", "/assets/app.js", "/v1/no/such/route"} {
		ran := false
		h := httpadapter.RequireScopes(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { ran = true }))
		c := core.Caller{Kind: core.CallerAgent, Name: "scout", ByToken: true}
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(core.WithCaller(t.Context(), c), http.MethodGet, path, nil))
		if !ran {
			t.Errorf("%s was refused", path)
		}
	}
}

func TestAllScopes_IsOneAdminScopePerGroup(t *testing.T) {
	t.Parallel()
	all := contract.AllScopes()
	if !slices.IsSorted(all) || !slices.Contains(all, "mcp:admin") || !slices.Contains(all, "tasks:admin") {
		t.Fatalf("AllScopes() = %v", all)
	}
	for _, s := range all {
		if !strings.HasSuffix(s, ":admin") {
			t.Errorf("AllScopes() holds %q", s)
		}
	}
}
