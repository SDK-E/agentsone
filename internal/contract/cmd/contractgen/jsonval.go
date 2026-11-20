// SPDX-License-Identifier: MIT

package main

// as reads a decoded JSON value as T. An absent value or one of another type
// reads as T's zero value: every optional OpenAPI field is read this way, and
// the fields the generator needs are checked where they are used.
func as[T any](v any) T {
	t, ok := v.(T)
	if !ok {
		var zero T
		return zero
	}
	return t
}

// object reads a decoded JSON value as an object; anything else is an empty one.
func object(v any) map[string]any { return as[map[string]any](v) }

// list reads a decoded JSON value as an array; anything else is an empty one.
func list(v any) []any { return as[[]any](v) }
