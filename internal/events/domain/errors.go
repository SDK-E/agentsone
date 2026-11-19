// SPDX-License-Identifier: MIT

package domain

import (
	"errors"
	"fmt"

	"github.com/SDK-E/agentsone/internal/core"
)

// Domain errors. Most wrap a core sentinel so the API layer maps them to a status.
var (
	ErrInvalidType     = fmt.Errorf("%w: event type must look like agentsone.<emitter>.<event>", core.ErrInvalid)
	ErrInvalidData     = fmt.Errorf("%w: event data must be a JSON object", core.ErrInvalid)
	ErrInvalidSubject  = fmt.Errorf("%w: subject must be 1 to 1024 characters", core.ErrInvalid)
	ErrInvalidLink     = fmt.Errorf("%w: causationid and correlationid must be prefixed ids", core.ErrInvalid)
	ErrInvalidSource   = fmt.Errorf("%w: source must be /agents/<name>, /emitters/<name>, /daemon or /host", core.ErrInvalid)
	ErrInvalidActor    = fmt.Errorf("%w: caller has no usable name", core.ErrInvalid)
	ErrInvalidEmitter  = fmt.Errorf("%w: invalid emitter", core.ErrInvalid)
	ErrInvalidTrigger  = fmt.Errorf("%w: invalid trigger", core.ErrInvalid)
	ErrInvalidSequence = fmt.Errorf("%w: sequence must be a number", core.ErrInvalid)
	ErrSourceForbidden = fmt.Errorf("%w: caller may not publish as that source", core.ErrForbidden)
	ErrTypeReserved    = fmt.Errorf("%w: built-in event types come from their emitter", core.ErrForbidden)
	ErrEmitterDisabled = fmt.Errorf("%w: emitter is disabled", core.ErrForbidden)
	ErrTypeNotAllowed  = fmt.Errorf("%w: emitter may not publish that event type", core.ErrForbidden)
	ErrNameTaken       = fmt.Errorf("%w: name is already used", core.ErrConflict)
	// ErrTooLarge is not a core sentinel: it maps to 413.
	ErrTooLarge = errors.New("payload too large")
)
