// SPDX-License-Identifier: MIT

package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

// ProblemTypeBase prefixes every problem type. The contract spells a type as
// the URN urn:agentsone:problem:<slug>.
const ProblemTypeBase = "urn:agentsone:problem:"

// Problem is an RFC 9457 problem details document. It is also an error, so
// handlers can return it directly.
type Problem struct {
	Type     string `json:"type"`
	Title    string `json:"title"`
	Status   int    `json:"status"`
	Detail   string `json:"detail,omitempty"`
	Instance string `json:"instance,omitempty"`
}

// Error returns the title and detail.
func (p *Problem) Error() string {
	if p.Detail == "" {
		return p.Title
	}
	return p.Title + ": " + p.Detail
}

// Sentinel errors. Wrap them with fmt.Errorf("...: %w", ErrNotFound) and the
// API layer maps them to the matching status.
var (
	ErrInvalid        = errors.New("invalid request")
	ErrUnauthorized   = errors.New("not signed in")
	ErrForbidden      = errors.New("not allowed")
	ErrNotFound       = errors.New("not found")
	ErrConflict       = errors.New("conflict")
	ErrNotImplemented = errors.New("not implemented")
	ErrUnavailable    = errors.New("not available yet")
	ErrBudget         = errors.New("budget exhausted")
	ErrFrozen         = errors.New("frozen")
)

var sentinelProblems = []struct {
	err    error
	slug   string
	status int
}{
	{ErrInvalid, "validation", http.StatusBadRequest},
	{ErrUnauthorized, "unauthenticated", http.StatusUnauthorized},
	{ErrBudget, "budget-exhausted", http.StatusPaymentRequired},
	{ErrForbidden, "forbidden", http.StatusForbidden},
	{ErrNotFound, "not-found", http.StatusNotFound},
	{ErrConflict, "conflict", http.StatusConflict},
	{ErrFrozen, "frozen", http.StatusLocked},
	{ErrNotImplemented, "not-implemented", http.StatusNotImplemented},
	{ErrUnavailable, "unavailable", http.StatusServiceUnavailable},
}

// NewProblem builds a problem with a type slug, for example "not-found".
func NewProblem(status int, slug, detail string) *Problem {
	return &Problem{
		Type:   ProblemTypeBase + slug,
		Title:  http.StatusText(status),
		Status: status,
		Detail: detail,
	}
}

// NotImplemented is the problem for an operation no module serves yet.
func NotImplemented(operationID string) *Problem {
	return NewProblem(http.StatusNotImplemented, "not-implemented",
		fmt.Sprintf("operation %s is not implemented yet", operationID))
}

// ProblemFrom maps any error to a problem: a *Problem passes through, a
// wrapped sentinel gets its status, anything else is a 500 that hides the
// error text.
func ProblemFrom(err error) *Problem {
	var p *Problem
	if errors.As(err, &p) {
		return p
	}
	for _, s := range sentinelProblems {
		if errors.Is(err, s.err) {
			return NewProblem(s.status, s.slug, err.Error())
		}
	}
	return NewProblem(http.StatusInternalServerError, "internal", "")
}

// WriteProblem writes p as application/problem+json.
func WriteProblem(w http.ResponseWriter, r *http.Request, p *Problem) {
	if p.Instance == "" && r != nil {
		p.Instance = r.URL.Path
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(p.Status)
	_ = json.NewEncoder(w).Encode(p) // the status is already sent; nothing useful to do on error
}

// WriteError maps err with ProblemFrom and writes it.
func WriteError(w http.ResponseWriter, r *http.Request, err error) {
	WriteProblem(w, r, ProblemFrom(err))
}
