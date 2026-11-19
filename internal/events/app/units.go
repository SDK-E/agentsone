// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"fmt"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/events/domain"
)

// NoUnits is the default UnitController. The daemon runs in a container
// without systemd, so a process emitter cannot run as a unit: registering
// one is refused as invalid. The supervisor can supply a real controller
// later without a change to this package.
type NoUnits struct{}

var _ UnitController = NoUnits{}

// Available is always false.
func (NoUnits) Available() bool { return false }

// Start always fails with core.ErrUnavailable.
func (NoUnits) Start(context.Context, domain.Emitter) error {
	return fmt.Errorf("process emitters need a unit controller: %w", core.ErrUnavailable)
}

// Stop does nothing: there is no unit to stop.
func (NoUnits) Stop(context.Context, domain.Emitter) error { return nil }
