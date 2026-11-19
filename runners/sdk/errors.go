// SPDX-License-Identifier: MIT

package sdk

import (
	"encoding/json"
	"errors"
	"fmt"
)

// Error codes of the runner protocol (PROTOCOL.md, Errors).
const (
	CodeParseError         = -32700
	CodeInvalidRequest     = -32600
	CodeMethodNotFound     = -32601
	CodeInvalidParams      = -32602
	CodeInternalError      = -32603
	CodeOutOfOrder         = -32000
	CodeLineTooLarge       = -32001
	CodeUnsupportedVersion = -32002
	CodeUnknownTurn        = 1001
	CodeUnknownRecipient   = 1002
	CodeNotPermitted       = 1003
	CodeUnknownTask        = 1004
	CodeBudgetExhausted    = 1010
	CodeResumeUnavailable  = 1011
	CodeModelUnavailable   = 1020
	CodeRateLimited        = 1030
)

// RPCError is a JSON-RPC error object received from the daemon.
type RPCError struct {
	Code    int
	Message string
	Data    json.RawMessage
}

// Error implements error.
func (e *RPCError) Error() string {
	return fmt.Sprintf("rpc error %d: %s", e.Code, e.Message)
}

// Errors a handler can return or the SDK reports.
var (
	// ErrBudgetExhausted is returned by a handler when the model gateway refused
	// a call for budget. The SDK pauses the turn (rule R1) and resumes it on the
	// budget.restored wake (rule R2).
	ErrBudgetExhausted = errors.New("sdk: budget exhausted")

	// ErrInvalidOneLine means a profile line is empty, longer than 255
	// characters, or has a line break.
	ErrInvalidOneLine = errors.New("sdk: one-line must be 1 to 255 characters with no line break")

	// ErrOutputClosed means output was sent after the final chunk of the turn
	// (rule T5).
	ErrOutputClosed = errors.New("sdk: output already finished for this turn")

	// ErrLineTooLarge means an outgoing message is longer than the line limit.
	ErrLineTooLarge = errors.New("sdk: message is larger than the line limit")

	// ErrDaemonClosed means the daemon closed the socket without shutdown
	// (rule X2); a runner main exits non-zero.
	ErrDaemonClosed = errors.New("sdk: daemon closed the connection")

	// ErrUnsupportedVersion means the daemon speaks none of our protocol
	// versions (handshake step 3).
	ErrUnsupportedVersion = errors.New("sdk: no common protocol version")
)

// UnsupportedVersionError carries the versions the daemon listed.
type UnsupportedVersionError struct{ Supported []string }

// Error implements error.
func (e *UnsupportedVersionError) Error() string {
	return fmt.Sprintf("sdk: no common protocol version, daemon supports %v", e.Supported)
}

// Unwrap lets errors.Is match ErrUnsupportedVersion.
func (e *UnsupportedVersionError) Unwrap() error { return ErrUnsupportedVersion }
