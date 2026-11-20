// SPDX-License-Identifier: MIT

package core

import (
	"context"
	"slices"
)

// CallerKind says what sort of principal made a request.
type CallerKind string

// Caller kinds.
const (
	// CallerAgent is an agent, identified by socket peer credentials and
	// its cgroup (agent@<name>.service), or by an agent token.
	CallerAgent CallerKind = "agent"
	// CallerHuman is a signed-in human, identified by a session cookie or token.
	CallerHuman CallerKind = "human"
	// CallerSystem is the daemon itself or one of its own units.
	CallerSystem CallerKind = "system"
	// CallerHost is the host tool on the Mac, reaching the daemon through the door.
	CallerHost CallerKind = "host"
	// CallerPlugin is an installed plugin, for example its emitter; Name is its plugin id.
	CallerPlugin CallerKind = "plugin"
)

// Caller is who made a request. The API layer sets it on every request
// context; modules read it with CallerFrom and never trust request bodies
// for identity.
type Caller struct {
	Kind CallerKind
	// Name is the agent name, human user name, unit name or "host".
	Name string
	// UID is the Linux user id seen on the socket, or -1 when unknown (TCP).
	UID int
	// IsRoot is true for root agents and for humans acting as root.
	IsRoot bool
	// Scopes are the permissions granted to a token or session.
	Scopes []string
	// Assurance says how strongly the identity is known. The zero value is
	// hard; agents named by uid or by token are soft.
	Assurance Assurance
}

// Assurance is how strongly a caller's identity is known.
type Assurance string

// Assurance levels.
const (
	// AssuranceHard: the kernel placed the process (agent cgroup), or a
	// human signed in to a daemon session, or a system caller.
	AssuranceHard Assurance = ""
	// AssuranceSoft: an agent named by its uid or by a bearer token, where
	// the process could not be placed in its own cgroup, or a human named
	// by their Linux user's uid on the socket (a root agent can take any uid).
	AssuranceSoft Assurance = "soft"
)

// Has reports whether the caller holds scope. Root callers hold every scope.
func (c Caller) Has(scope string) bool {
	return c.IsRoot || slices.Contains(c.Scopes, scope)
}

// Anonymous is the caller when none was established.
var Anonymous = Caller{Kind: CallerHuman, Name: "", UID: -1}

type callerKey struct{}

// WithCaller returns ctx carrying c. Only the API layer should call it.
func WithCaller(ctx context.Context, c Caller) context.Context {
	return context.WithValue(ctx, callerKey{}, c)
}

// CallerFrom returns the caller on ctx, and false when none was set.
func CallerFrom(ctx context.Context) (Caller, bool) {
	c, ok := ctx.Value(callerKey{}).(Caller)
	if !ok {
		return Anonymous, false
	}
	return c, true
}
