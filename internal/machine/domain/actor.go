// SPDX-License-Identifier: MIT

package domain

import (
	"errors"
	"fmt"

	"github.com/SDK-E/agentsone/internal/core"
)

// ActorKind says who asks the machine to do something.
type ActorKind string

// Actor kinds.
const (
	ActorHuman  ActorKind = "human"
	ActorAgent  ActorKind = "agent"
	ActorSystem ActorKind = "system"
)

// Actor is the caller of a machine operation, in machine words.
type Actor struct {
	Kind ActorKind
	Name string
	// UID is the caller's Linux uid, or -1 when unknown (humans over TCP).
	UID int
	// IsRoot is true for root agents.
	IsRoot bool
	// Home is a custom agent's home folder; empty for everyone else.
	Home string
}

// Label is the actor as "<kind>:<name>", the form audit and events use.
func (a Actor) Label() string { return string(a.Kind) + ":" + a.Name }

// Privileged reports whether the actor holds the whole machine. Humans own
// the machine, root agents are uid 0 and the daemon is itself; custom agents
// are held to their own user.
func (a Actor) Privileged() bool {
	return a.IsRoot || a.Kind == ActorHuman || a.Kind == ActorSystem
}

// RunAsUID is the Linux uid an operation runs as: root for privileged
// actors, the agent's own uid otherwise.
func (a Actor) RunAsUID() int {
	if a.Privileged() {
		return 0
	}
	return a.UID
}

// ErrTooLarge is a body or file over a machine size cap.
var ErrTooLarge = errors.New("too large")

func forbidden(format string, args ...any) error {
	return fmt.Errorf("%s: %w", fmt.Sprintf(format, args...), core.ErrForbidden)
}

func invalid(format string, args ...any) error {
	return fmt.Errorf("%s: %w", fmt.Sprintf(format, args...), core.ErrInvalid)
}
