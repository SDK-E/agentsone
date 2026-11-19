// SPDX-License-Identifier: MIT

package domain

import "fmt"

// PrincipalKind says what sort of caller reads or writes memory.
type PrincipalKind string

// Principal kinds.
const (
	KindAgent  PrincipalKind = "agent"
	KindHuman  PrincipalKind = "human"
	KindSystem PrincipalKind = "system" // the daemon itself and the host tool
	KindPlugin PrincipalKind = "plugin"
)

// Principal is who acts on memory. Team is the head of the agent's chart
// branch, empty for kinds other than agent.
type Principal struct {
	Kind PrincipalKind
	Name string
	Team string
	Root bool
}

// Visibility is the set of records a principal may read. The store applies it
// in its query, so pages hold only readable records.
type Visibility struct {
	// All lets the reader see every record.
	All bool
	// Owner is the agent whose private records are visible.
	Owner string
	// Team is the chart branch whose team records are visible.
	Team string
}

// VisibilityOf returns what p may read. Root agents, humans and the system
// read all; plugins read common memories only; other agents read common ones,
// their own private ones and their team's.
func VisibilityOf(p Principal) Visibility {
	switch {
	case p.Root, p.Kind == KindHuman, p.Kind == KindSystem:
		return Visibility{All: true}
	case p.Kind == KindAgent:
		return Visibility{Owner: p.Name, Team: p.Team}
	default:
		return Visibility{}
	}
}

// Allows reports whether r is visible.
func (v Visibility) Allows(r Record) bool {
	switch {
	case v.All, r.Scope == Common:
		return true
	case r.Scope == Private:
		return v.Owner != "" && r.Owner == v.Owner
	case r.Scope == Team:
		return v.Team != "" && r.Team == v.Team
	default:
		return false
	}
}

// CanRead reports whether p may read r.
func CanRead(p Principal, r Record) bool { return VisibilityOf(p).Allows(r) }

// CanWrite checks that p may write a record of scope. Only agents write, and
// only their own records; nobody writes another agent's private memory. A team
// memory needs a chart branch to belong to.
func CanWrite(p Principal, scope Scope) error {
	if p.Kind != KindAgent {
		return fmt.Errorf("%w: only agents write memory", ErrNotAllowed)
	}
	if scope == Team && p.Team == "" {
		return fmt.Errorf("%w: %s has no chart branch for a team memory", ErrNotAllowed, p.Name)
	}
	return nil
}

// CanDelete checks that p may delete r: its owner may, and so may any root.
func CanDelete(p Principal, r Record) error {
	if p.Root || (p.Kind == KindAgent && p.Name == r.Owner) {
		return nil
	}
	return fmt.Errorf("%w: only %s or a root agent deletes this record", ErrNotAllowed, r.Owner)
}
