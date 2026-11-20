// SPDX-License-Identifier: MIT

package rpc

import (
	"errors"
	"fmt"

	"github.com/SDK-E/agentsone/internal/agents/supervisor/domain"
)

// Error codes of the runner protocol (PROTOCOL.md errors).
const (
	CodeParseError         = -32700
	CodeInvalidRequest     = -32600
	CodeMethodNotFound     = -32601
	CodeInvalidParams      = -32602
	CodeInternal           = -32603
	CodeOutOfOrder         = -32000
	CodeLineTooLarge       = -32001
	CodeUnsupportedVersion = -32002
	CodeHeartbeatTimeout   = -32003
	CodeUnknownTurn        = 1001
	CodeUnknownRecipient   = 1002
	CodeNotPermitted       = 1003
	CodeUnknownTask        = 1004
	CodeResumeUnavailable  = 1011
	CodeRateLimited        = 1030
)

// RemoteError is a JSON-RPC error object, sent or received.
type RemoteError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func (e *RemoteError) Error() string { return fmt.Sprintf("runner error %d: %s", e.Code, e.Message) }

// RPCCode is the error code, for the supervisor's app layer, which reads the
// runner's answer without importing this package.
func (e *RemoteError) RPCCode() int { return e.Code }

// RPCMessage is the error message, read the same way as RPCCode.
func (e *RemoteError) RPCMessage() string { return e.Message }

var codeErrors = []struct {
	code    int
	message string
	err     error
}{
	{CodeUnknownTurn, "Unknown turn", domain.ErrUnknownTurn},
	{CodeUnknownRecipient, "Unknown recipient", domain.ErrUnknownRecipient},
	{CodeNotPermitted, "Not permitted", domain.ErrNotPermitted},
	{CodeUnknownTask, "Unknown task", domain.ErrUnknownTask},
	{CodeResumeUnavailable, "Resume unavailable", domain.ErrResumeUnavailable},
	{CodeRateLimited, "Rate limited", domain.ErrRateLimited},
}

// Is lets errors.Is match a runner's error code against the domain errors,
// for example a 1011 answer against domain.ErrResumeUnavailable.
func (e *RemoteError) Is(target error) bool {
	for _, c := range codeErrors {
		if c.code == e.Code && c.err == target {
			return true
		}
	}
	return false
}

// FromError maps a handler error to its protocol error. Unknown errors are
// -32603 with a fixed message, so no internal detail reaches the runner; a
// domain.StepError adds the name of the step that failed, never its cause.
func FromError(err error) *RemoteError {
	var re *RemoteError
	if errors.As(err, &re) {
		return re
	}
	for _, c := range codeErrors {
		if errors.Is(err, c.err) {
			return &RemoteError{Code: c.code, Message: c.message}
		}
	}
	if se, ok := errors.AsType[*domain.StepError](err); ok {
		return &RemoteError{Code: CodeInternal, Message: "Internal error: could not " + se.Step}
	}
	return &RemoteError{Code: CodeInternal, Message: "Internal error"}
}
