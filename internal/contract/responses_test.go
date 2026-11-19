// SPDX-License-Identifier: MIT

package contract_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

var operationMethods = []string{"get", "put", "post", "delete", "options", "head", "patch", "trace"}

type bundledSpec struct {
	Paths      map[string]map[string]json.RawMessage `json:"paths"`
	Components struct {
		Responses map[string]struct {
			Headers map[string]json.RawMessage `json:"headers"`
		} `json:"responses"`
	} `json:"components"`
}

type bundledOp struct {
	OperationID string `json:"operationId"`
	Responses   map[string]struct {
		Ref     string                     `json:"$ref"`
		Headers map[string]json.RawMessage `json:"headers"`
	} `json:"responses"`
}

func loadBundledSpec(t *testing.T) bundledSpec {
	t.Helper()
	raw, err := os.ReadFile(contractPath(t, "dist/openapi.json")) // #nosec G304 -- contract file
	if err != nil {
		t.Fatal(err)
	}
	var spec bundledSpec
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}
	return spec
}

func eachOperation(t *testing.T, spec bundledSpec, fn func(path, method string, op bundledOp)) {
	t.Helper()
	for path, item := range spec.Paths {
		for _, method := range operationMethods {
			rawOp, ok := item[method]
			if !ok {
				continue
			}
			var op bundledOp
			if err := json.Unmarshal(rawOp, &op); err != nil {
				t.Fatalf("%s %s: %v", method, path, err)
			}
			fn(path, method, op)
		}
	}
}

// TestEveryOperationDeclares501 checks the bundle step that adds the shared
// not-implemented response to every operation.
func TestEveryOperationDeclares501(t *testing.T) {
	spec := loadBundledSpec(t)
	n := 0
	eachOperation(t, spec, func(path, method string, op bundledOp) {
		n++
		resp, ok := op.Responses["501"]
		if !ok {
			t.Errorf("%s %s (%s): no 501 response", method, path, op.OperationID)
			return
		}
		if resp.Ref != "#/components/responses/NotImplemented" {
			t.Errorf("%s %s (%s): 501 is %q, want the shared NotImplemented response", method, path, op.OperationID, resp.Ref)
		}
	})
	if n == 0 {
		t.Fatal("no operations found in contract/dist/openapi.json")
	}
	if _, ok := spec.Components.Responses["NotImplemented"]; !ok {
		t.Error("components.responses.NotImplemented is missing")
	}
}

// TestEvery429DeclaresRetryAfter checks that clients can always read how long
// to wait: each 429 response, inline or shared, carries a Retry-After header.
func TestEvery429DeclaresRetryAfter(t *testing.T) {
	spec := loadBundledSpec(t)
	eachOperation(t, spec, func(path, method string, op bundledOp) {
		resp, ok := op.Responses["429"]
		if !ok {
			return
		}
		headers := resp.Headers
		if resp.Ref != "" {
			name := strings.TrimPrefix(resp.Ref, "#/components/responses/")
			headers = spec.Components.Responses[name].Headers
		}
		if _, ok := headers["Retry-After"]; !ok {
			t.Errorf("%s %s (%s): 429 has no Retry-After header", method, path, op.OperationID)
		}
	})
}
