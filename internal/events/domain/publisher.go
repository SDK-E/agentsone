// SPDX-License-Identifier: MIT

package domain

import (
	"regexp"

	"github.com/SDK-E/agentsone/internal/core"
)

// actorRefPattern is the event schema's ActorRef: <kind>:<name>.
var actorRefPattern = regexp.MustCompile(`^((agent|human|system|host):[a-z_][a-z0-9_-]{0,31}|plugin:plg_[0-7][0-9A-HJKMNP-TV-Z]{25})$`)

// Privileged reports whether c speaks for the machine: the daemon's own
// modules, the host, or a root caller placed by the kernel.
func Privileged(c core.Caller) bool {
	return c.IsRoot || c.Kind == core.CallerSystem || c.Kind == core.CallerHost
}

// AuthoriseSource checks that c may publish an event of eventType as source,
// for every source but an emitter's ("/emitters/<name>"), whose rules need
// the emitter's record (see Emitter.MayPublish).
//
// Built-in event types state facts only the daemon can vouch for, such as a
// hire or a sign-in, so only a privileged caller publishes them. A daemon
// module that announces such a fact publishes as itself once its own rules
// have passed, not in the context of the caller that asked for it, and names
// that caller as the actor (see PortActor).
func AuthoriseSource(c core.Caller, source, eventType string) error {
	if Privileged(c) {
		return nil
	}
	if BuiltinEmitterType(eventType) {
		return ErrTypeReserved
	}
	switch {
	case source == "/agents/"+c.Name && c.Kind == core.CallerAgent:
		return nil
	case source == "/daemon" && c.Kind == core.CallerHuman:
		return nil
	default:
		return ErrSourceForbidden
	}
}

// PortActor is the actor written on an event published through the port that
// daemon modules use. A daemon module (a system caller) names who caused the
// fact it announces, for example the agent that hired; when it names no one
// it is the actor itself. Every other caller is the actor of what it
// publishes, whatever the event says.
func PortActor(c core.Caller, named string) (string, error) {
	if c.Kind != core.CallerSystem || named == "" {
		return EventActor(c)
	}
	if !actorRefPattern.MatchString(named) {
		return "", ErrInvalidActor
	}
	return named, nil
}
