// SPDX-License-Identifier: MIT

package domain

import "fmt"

// Scope says who may read a memory record.
type Scope string

// Memory scopes, with the contract's values.
const (
	// Private is the owner only.
	Private Scope = "private"
	// Team is every agent in the owner's chart branch.
	Team Scope = "team"
	// Common is every agent.
	Common Scope = "common"
)

// ParseScope checks s is one of the three scopes.
func ParseScope(s string) (Scope, error) {
	switch sc := Scope(s); sc {
	case Private, Team, Common:
		return sc, nil
	default:
		return "", fmt.Errorf("%w: scope %q, want private, team or common", ErrInvalid, s)
	}
}
