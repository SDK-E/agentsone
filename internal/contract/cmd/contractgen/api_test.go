// SPDX-License-Identifier: MIT

package main

import "testing"

func TestNewOperation_NeedsAnIDAndATextTag(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		raw  map[string]any
		ok   bool
	}{
		{"complete", map[string]any{"operationId": "getA", "tags": []any{"agents"}}, true},
		{"no operation id", map[string]any{"tags": []any{"agents"}}, false},
		{"no tags", map[string]any{"operationId": "getA"}, false},
		{"tag is not text", map[string]any{"operationId": "getA", "tags": []any{7.0}}, false},
		{"tag is empty", map[string]any{"operationId": "getA", "tags": []any{""}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			op, err := newOperation("get", "/a", tt.raw)
			if (err == nil) != tt.ok {
				t.Fatalf("newOperation() error = %v, want ok = %v", err, tt.ok)
			}
			if tt.ok && op.Tag != "agents" {
				t.Fatalf("Tag = %q, want agents", op.Tag)
			}
		})
	}
}
