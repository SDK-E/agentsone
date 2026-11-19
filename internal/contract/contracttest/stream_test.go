// SPDX-License-Identifier: MIT

package contracttest_test

import (
	"testing"

	"github.com/SDK-E/agentsone/internal/contract/contracttest"
)

const streamSpec = `{
  "openapi": "3.1.0",
  "info": {"title": "t", "version": "1"},
  "servers": [{"url": "http://127.0.0.1:7700/v1"}],
  "paths": {
    "/things/stream": {
      "get": {
        "operationId": "streamThings",
        "tags": ["things"],
        "responses": {
          "200": {"description": "frames", "content": {"text/event-stream": {"schema": {"$ref": "#/components/schemas/Frame"}}}}
        }
      }
    },
    "/things": {
      "get": {
        "operationId": "listThings",
        "tags": ["things"],
        "responses": {"200": {"description": "ok", "content": {"application/json": {"schema": {"type": "object"}}}}}
      }
    }
  },
  "components": {
    "schemas": {
      "Frame": {"type": "object", "required": ["kind"], "properties": {"kind": {"enum": ["a", "b"]}}, "additionalProperties": false}
    }
  }
}`

func TestValidateStreamFrame(t *testing.T) {
	v, err := contracttest.NewValidator([]byte(streamSpec))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name    string
		op      string
		body    string
		wantErr bool
	}{
		{"valid frame", "streamThings", `{"kind":"a"}`, false},
		{"frame breaks the schema", "streamThings", `{"kind":"z"}`, true},
		{"extra property", "streamThings", `{"kind":"a","x":1}`, true},
		{"not json", "streamThings", `{`, true},
		{"unknown operation", "nope", `{}`, true},
		{"not a stream", "listThings", `{}`, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := v.ValidateStreamFrame(c.op, []byte(c.body))
			if (err != nil) != c.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, c.wantErr)
			}
		})
	}
}
