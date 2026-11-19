// SPDX-License-Identifier: MIT

package api

import (
	"fmt"
	"net/http"

	contract "github.com/SDK-E/agentsone/internal/contract/api"
	"github.com/SDK-E/agentsone/internal/core"
)

// Compose mounts every module's groups and routes on one handler. Contract
// operations no module serves answer 501, other paths 404, both as
// problem+json. A tag served twice, a tag outside the contract or two
// conflicting routes are errors. Owned by work package 0 (frozen seam);
// work package 2 wraps the result with caller identity.
func Compose(mods []core.Module) (http.Handler, error) {
	mux := http.NewServeMux()
	if err := mountGroups(mux, mods); err != nil {
		return nil, err
	}
	if err := mountRoutes(mux, mods); err != nil {
		return nil, err
	}
	mux.HandleFunc("/", notServed)
	return mux, nil
}

// BadRequest writes the 400 problem for a request the generated code could
// not decode.
func BadRequest(w http.ResponseWriter, r *http.Request, err error) {
	core.WriteProblem(w, r, core.NewProblem(http.StatusBadRequest, "invalid", err.Error()))
}

func mountGroups(mux *http.ServeMux, mods []core.Module) error {
	known := contractTags()
	owner := map[string]string{}
	opts := core.APIOptions{RequestError: BadRequest, ResponseError: core.WriteError}
	for _, m := range mods {
		for _, g := range m.API().Groups {
			if !known[g.Tag] {
				return fmt.Errorf("module %s serves tag %q, which is not in the contract", m.Name(), g.Tag)
			}
			if prev, taken := owner[g.Tag]; taken {
				return fmt.Errorf("tag %q is served by both %s and %s", g.Tag, prev, m.Name())
			}
			owner[g.Tag] = m.Name()
			g.Register(mux, opts)
		}
	}
	return nil
}

func mountRoutes(mux *http.ServeMux, mods []core.Module) error {
	for _, m := range mods {
		for _, r := range m.API().Routes {
			if err := handle(mux, r); err != nil {
				return fmt.Errorf("module %s route %q: %w", m.Name(), r.Pattern, err)
			}
		}
	}
	return nil
}

// handle turns the ServeMux panic on a conflicting pattern into an error.
func handle(mux *http.ServeMux, r core.Route) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("%v", p)
		}
	}()
	mux.Handle(r.Pattern, r.Handler)
	return nil
}

func contractTags() map[string]bool {
	tags := map[string]bool{}
	for _, r := range contract.Routes {
		tags[r.Tag] = true
	}
	return tags
}

func notServed(w http.ResponseWriter, r *http.Request) {
	if route, ok := contract.Match(r.Method, r.URL.Path); ok {
		core.WriteProblem(w, r, core.NotImplemented(route.OperationID))
		return
	}
	core.WriteProblem(w, r, core.NewProblem(http.StatusNotFound, "not-found", "no such endpoint"))
}
