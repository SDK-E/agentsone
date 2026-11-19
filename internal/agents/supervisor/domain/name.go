// SPDX-License-Identifier: MIT

package domain

import (
	"errors"
	"fmt"
)

// ErrBadName is returned for an agent name that is not a contract AgentName.
var ErrBadName = errors.New("agent name must be a lowercase letter, then up to 30 lowercase letters, digits or hyphens")

// MaxNameLen is the longest agent name. It is also the Linux user name of a
// custom agent, so it stays within the 32 bytes useradd allows.
const MaxNameLen = 31

// ValidateName checks that name matches the contract AgentName pattern
// ^[a-z][a-z0-9-]{0,30}$. The name ends up in unit names, file paths and user
// names, so anything else (a slash, a dot, a space) is refused here.
func ValidateName(name string) error {
	if name == "" || len(name) > MaxNameLen {
		return fmt.Errorf("%q: %w", name, ErrBadName)
	}
	if name[0] < 'a' || name[0] > 'z' {
		return fmt.Errorf("%q: %w", name, ErrBadName)
	}
	for i := 1; i < len(name); i++ {
		c := name[i]
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
			return fmt.Errorf("%q: %w", name, ErrBadName)
		}
	}
	return nil
}
