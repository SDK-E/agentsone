// SPDX-License-Identifier: MIT

package httpadapter

import (
	"net/http"
	"slices"

	contract "github.com/SDK-E/agentsone/internal/contract/api"
	"github.com/SDK-E/agentsone/internal/core"
)

// RequireScopes refuses a call made with a bearer token that does not hold
// the scope the contract names for the operation, with 403 forbidden, before
// next runs. A token sent to an operation whose security takes no token is
// refused the same way. Callers named by a session cookie or by socket peer
// credentials carry no scopes and pass: caller kind and org chart rules in
// the modules decide for them. Public operations and paths outside the
// contract (the interface, unknown paths answered 404) pass too.
//
// It wraps the composed modules, so every way in is checked: both listeners
// and in-process dispatch such as machine MCP tool calls, which carry the
// token's caller.
func RequireScopes(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, ok := core.CallerFrom(r.Context())
		if !ok || !c.ByToken {
			next.ServeHTTP(w, r)
			return
		}
		route, ok := contract.Match(r.Method, r.URL.Path)
		if !ok || route.Public {
			next.ServeHTTP(w, r)
			return
		}
		if route.Scope == "" {
			core.WriteProblem(w, r, core.NewProblem(http.StatusForbidden, "forbidden",
				route.OperationID+" takes no token; sign in or call over the socket"))
			return
		}
		if !slices.ContainsFunc(c.Scopes, func(held string) bool { return contract.Covers(held, route.Scope) }) {
			core.WriteProblem(w, r, core.NewProblem(http.StatusForbidden, "forbidden",
				"this token does not hold the "+route.Scope+" scope that "+route.OperationID+" needs"))
			return
		}
		next.ServeHTTP(w, r)
	})
}
