// SPDX-License-Identifier: MIT

package main

// Every operation may answer 501 not-implemented: the contract lists operations
// before the daemon serves them. Writing the same response into every source
// operation would be noise, so the bundle step adds one shared component and a
// reference to it on each operation. The sources stay unchanged.

const (
	notImplementedName = "NotImplemented"
	notImplementedRef  = "#/components/responses/" + notImplementedName
)

// injectNotImplemented adds the shared 501 response component and a "501"
// response on every operation that does not declare one itself.
func injectNotImplemented(doc map[string]any) {
	components, ok := doc["components"].(map[string]any)
	if !ok {
		components = map[string]any{}
		doc["components"] = components
	}
	responses, ok := components["responses"].(map[string]any)
	if !ok {
		responses = map[string]any{}
		components["responses"] = responses
	}
	responses[notImplementedName] = map[string]any{
		"description": "The operation is in the contract but this daemon does not serve it yet (`urn:agentsone:problem:not-implemented`).",
		"content": map[string]any{
			"application/problem+json": map[string]any{
				"schema": map[string]any{"$ref": schemaRefPrefix + "Problem"},
			},
		},
	}
	paths, _ := doc["paths"].(map[string]any)
	for _, item := range paths {
		pathItem, _ := item.(map[string]any)
		for _, method := range httpMethods {
			op, ok := pathItem[method].(map[string]any)
			if !ok {
				continue
			}
			opResponses, ok := op["responses"].(map[string]any)
			if !ok {
				opResponses = map[string]any{}
				op["responses"] = opResponses
			}
			if _, has := opResponses["501"]; !has {
				opResponses["501"] = map[string]any{"$ref": notImplementedRef}
			}
		}
	}
}
