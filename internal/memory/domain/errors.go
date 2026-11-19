// SPDX-License-Identifier: MIT

package domain

import "errors"

// Domain errors. The app layer maps them to core sentinels.
var (
	// ErrInvalid is a write the rules refuse: a bad scope, title, tag or key.
	ErrInvalid = errors.New("invalid memory")
	// ErrTooLarge is a body above the size limit.
	ErrTooLarge = errors.New("memory body too large")
	// ErrNotAllowed is an action the caller's role does not permit.
	ErrNotAllowed = errors.New("memory action not allowed")
	// ErrNotFound is a record that does not exist.
	ErrNotFound = errors.New("memory record not found")
)
