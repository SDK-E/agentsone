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
			op, err := newOperation("get", "/a", tt.raw, nil)
			if (err == nil) != tt.ok {
				t.Fatalf("newOperation() error = %v, want ok = %v", err, tt.ok)
			}
			if tt.ok && op.Tag != "agents" {
				t.Fatalf("Tag = %q, want agents", op.Tag)
			}
		})
	}
}

func TestNewOperation_ReadsTheBearerScope(t *testing.T) {
	t.Parallel()
	doc := []any{map[string]any{"cookieAuth": []any{}}, map[string]any{"bearerAuth": []any{}}}
	base := func(security any) map[string]any {
		raw := map[string]any{"operationId": "getA", "tags": []any{"agents"}}
		if security != nil {
			raw["security"] = security
		}
		return raw
	}
	tests := []struct {
		name     string
		security any
		scope    string
		public   bool
		ok       bool
	}{
		{"own scope", []any{map[string]any{"cookieAuth": []any{}}, map[string]any{"bearerAuth": []any{"agents:read"}}}, "agents:read", false, true},
		{"inherits the document's empty scope", nil, "", false, false},
		{"own empty scope", []any{map[string]any{"bearerAuth": []any{}}}, "", false, false},
		{"no bearer", []any{map[string]any{"hostDoor": []any{}}}, "", false, true},
		{"public", []any{}, "", true, true},
		{"two scopes", []any{map[string]any{"bearerAuth": []any{"agents:read", "tasks:read"}}}, "", false, false},
		{"bearer twice", []any{map[string]any{"bearerAuth": []any{"agents:read"}}, map[string]any{"bearerAuth": []any{"agents:write"}}}, "", false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			op, err := newOperation("get", "/a", base(tt.security), doc)
			if (err == nil) != tt.ok {
				t.Fatalf("newOperation() error = %v, want ok = %v", err, tt.ok)
			}
			if tt.ok && (op.Scope != tt.scope || op.Public != tt.public) {
				t.Fatalf("Scope, Public = %q, %v, want %q, %v", op.Scope, op.Public, tt.scope, tt.public)
			}
		})
	}
}
