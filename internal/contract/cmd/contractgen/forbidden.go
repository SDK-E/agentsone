// SPDX-License-Identifier: MIT

package main

import "fmt"

// Every operation that needs a caller may answer 403 forbidden: the daemon
// refuses a bearer token that lacks the scope the operation's security names,
// before the handler runs. Writing the same response into every source
// operation would be noise, so the bundle step adds a reference to the shared
// Forbidden response on each such operation. The sources stay unchanged.

const (
	forbiddenName = "Forbidden"
	forbiddenRef  = "#/components/responses/" + forbiddenName
)

// injectForbidden adds a "403" response referencing the shared Forbidden
// component on every operation whose security (its own, else the document's)
// needs a caller and that does not declare a 403 itself. Public operations,
// with an empty security list or an empty requirement, are left alone.
func injectForbidden(doc map[string]any) error {
	responses := object(object(doc["components"])["responses"])
	if _, ok := responses[forbiddenName]; !ok {
		return fmt.Errorf("the bundle has no components.responses.%s to reference", forbiddenName)
	}
	for _, item := range object(doc["paths"]) {
		pathItem := object(item)
		for _, method := range httpMethods {
			op, ok := pathItem[method].(map[string]any)
			if !ok {
				continue
			}
			security, own := op["security"]
			if !own {
				security = doc["security"]
			}
			if !needsCaller(security) {
				continue
			}
			opResponses, ok := op["responses"].(map[string]any)
			if !ok {
				opResponses = map[string]any{}
				op["responses"] = opResponses
			}
			if _, has := opResponses["403"]; !has {
				opResponses["403"] = map[string]any{"$ref": forbiddenRef}
			}
		}
	}
	return nil
}

// needsCaller reports whether a security requirement list admits no
// anonymous caller: it is not empty and none of its requirements is {}.
func needsCaller(security any) bool {
	reqs := list(security)
	if len(reqs) == 0 {
		return false
	}
	for _, req := range reqs {
		if len(object(req)) == 0 {
			return false
		}
	}
	return true
}
