// SPDX-License-Identifier: MIT

package contract_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/SDK-E/agentsone/internal/contract/api"
	"github.com/SDK-E/agentsone/internal/core"
)

const problemTypePrefix = "urn:agentsone:problem:"

// contractProblemSlugs reads the problem types the contract lists on the
// Problem schema.
func contractProblemSlugs(t *testing.T) map[string]bool {
	t.Helper()
	var doc struct {
		Components struct {
			Schemas map[string]struct {
				Description string `json:"description"`
			} `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(api.Spec, &doc); err != nil {
		t.Fatal(err)
	}
	desc := doc.Components.Schemas["Problem"].Description
	slugs := map[string]bool{}
	for _, m := range regexp.MustCompile("`" + problemTypePrefix + `([a-z-]+)` + "`").FindAllStringSubmatch(desc, -1) {
		slugs[m[1]] = true
	}
	if len(slugs) == 0 {
		t.Fatal("the Problem schema lists no problem types")
	}
	return slugs
}

// notListed are daemon answers the contract does not list yet: the 501 for an
// operation nobody serves (contract/README.md, "Errors").
var notListed = map[string]bool{"not-implemented": true}

func TestCoreProblemsUseContractTypes(t *testing.T) {
	slugs := contractProblemSlugs(t)
	errs := []error{
		core.ErrInvalid, core.ErrUnauthorized, core.ErrForbidden, core.ErrNotFound, core.ErrConflict,
		core.ErrNotImplemented, core.ErrUnavailable, core.ErrBudget, core.ErrFrozen,
		errors.New("something else"),
	}
	for _, err := range errs {
		p := core.ProblemFrom(fmt.Errorf("wrapped: %w", err))
		slug, ok := strings.CutPrefix(p.Type, problemTypePrefix)
		if !ok {
			t.Errorf("%v: type %q does not start with %s", err, p.Type, problemTypePrefix)
			continue
		}
		if !slugs[slug] && !notListed[slug] {
			t.Errorf("%v: type %q is not listed in the contract", err, p.Type)
		}
	}
}

func TestNewProblemEmitsURN(t *testing.T) {
	if got := core.NewProblem(http.StatusNotFound, "not-found", "").Type; got != "urn:agentsone:problem:not-found" {
		t.Fatalf("type %q", got)
	}
}
