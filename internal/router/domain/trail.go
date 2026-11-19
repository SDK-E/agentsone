// SPDX-License-Identifier: MIT

package domain

import (
	"fmt"
	"slices"

	"github.com/SDK-E/agentsone/internal/core"
)

// MaxHops is how many times a request may pass from one router to another.
const MaxHops = 3

// ErrHandoffLoop is returned for a hand-off to a router that already had the
// request, or past MaxHops.
var ErrHandoffLoop = fmt.Errorf("router hand-off refused: %w", core.ErrConflict)

// Hop is one hand-off between routers.
type Hop struct {
	Router   string
	HandedTo string
	Reason   string
}

// Trail follows one request through the routers that handled it.
type Trail struct {
	current string
	visited []string
	hops    []Hop
}

// NewTrail starts a trail at the first router.
func NewTrail(router string) *Trail {
	return &Trail{current: router, visited: []string{router}}
}

// Current is the router that has the request now.
func (t *Trail) Current() string { return t.current }

// Hops returns a copy of the hand-offs so far, in order.
func (t *Trail) Hops() []Hop { return slices.Clone(t.hops) }

// HandOff passes the request to another router. A router that already had
// the request, or a fourth hop, is ErrHandoffLoop: it stops routers that
// send a request back and forth.
func (t *Trail) HandOff(to, reason string) error {
	if slices.Contains(t.visited, to) {
		return fmt.Errorf("%s already had this request: %w", to, ErrHandoffLoop)
	}
	if len(t.hops) >= MaxHops {
		return fmt.Errorf("more than %d hand-offs: %w", MaxHops, ErrHandoffLoop)
	}
	t.hops = append(t.hops, Hop{Router: t.current, HandedTo: to, Reason: reason})
	t.visited = append(t.visited, to)
	t.current = to
	return nil
}
