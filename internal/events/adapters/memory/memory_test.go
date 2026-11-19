// SPDX-License-Identifier: MIT

package memory_test

import (
	"testing"

	"github.com/SDK-E/agentsone/internal/events/adapters/memory"
	"github.com/SDK-E/agentsone/internal/events/domain"
	"github.com/SDK-E/agentsone/internal/events/eventstest"
)

func TestEventsRepository(t *testing.T) {
	eventstest.RunEventContract(t, func(*testing.T) domain.EventRepository { return memory.NewEvents() })
}

func TestEmittersRepository(t *testing.T) {
	eventstest.RunEmitterContract(t, func(*testing.T) domain.EmitterRepository { return memory.NewEmitters() })
}

func TestTriggersRepository(t *testing.T) {
	eventstest.RunTriggerContract(t, func(*testing.T) domain.TriggerRepository { return memory.NewTriggers() })
}
