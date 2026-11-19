// SPDX-License-Identifier: MIT

package domain

import (
	"fmt"

	"github.com/SDK-E/agentsone/internal/core"
)

// Domain errors. Each wraps a core sentinel so the API layer maps it to a status.
var (
	ErrInvalidChannel = fmt.Errorf("%w: invalid channel", core.ErrInvalid)
	ErrNoTeam         = fmt.Errorf("%w: only an agent in the chart can own a team channel", core.ErrInvalid)
	ErrInvalidMessage = fmt.Errorf("%w: invalid message", core.ErrInvalid)
	ErrInvalidActor   = fmt.Errorf("%w: caller has no usable name", core.ErrInvalid)
	ErrInvalidTarget  = fmt.Errorf("%w: a bus target is agent:<name> or channel:<name>", core.ErrInvalid)
	ErrNameTaken      = fmt.Errorf("%w: channel name is already used", core.ErrConflict)
)
