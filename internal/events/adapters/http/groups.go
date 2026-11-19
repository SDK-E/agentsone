// SPDX-License-Identifier: MIT

package httpadapter

import (
	"github.com/SDK-E/agentsone/internal/contract/api/triggersapi"
	"github.com/SDK-E/agentsone/internal/core"
)

// Groups returns the events and triggers API groups over svc.
func Groups(svc Service, clock core.Clock, opts ...Option) []core.APIGroup {
	return []core.APIGroup{
		EventsGroup(NewEvents(svc, clock, opts...)),
		triggersapi.Group(NewTriggers(svc)),
	}
}
