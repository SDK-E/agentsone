// SPDX-License-Identifier: MIT

package adminapi

import (
	"encoding/json"
	"errors"
	"net/http"
)

// ProblemTypeBase prefixes every problem type.
const ProblemTypeBase = "urn:agentsone:problem:"

// Problem slugs the admin routes use.
const (
	SlugValidation = "validation"
	SlugNotFound   = "not-found"
	SlugConflict   = "conflict"
	SlugInternal   = "internal"
)

// Problem is an RFC 9457 problem document. It is also the error the client returns.
type Problem struct {
	Type   string `json:"type"`
	Title  string `json:"title"`
	Status int    `json:"status"`
	Detail string `json:"detail,omitempty"`
}

// Error returns the title and the detail.
func (p *Problem) Error() string {
	if p.Detail == "" {
		return p.Title
	}
	return p.Title + ": " + p.Detail
}

// Slug returns the problem type without its prefix.
func (p *Problem) Slug() string {
	if len(p.Type) > len(ProblemTypeBase) && p.Type[:len(ProblemTypeBase)] == ProblemTypeBase {
		return p.Type[len(ProblemTypeBase):]
	}
	return p.Type
}

// NewProblem builds a problem for status with a type slug.
func NewProblem(status int, slug, detail string) *Problem {
	return &Problem{Type: ProblemTypeBase + slug, Title: http.StatusText(status), Status: status, Detail: detail}
}

// IsProblem reports whether err is a problem with this slug.
func IsProblem(err error, slug string) bool {
	var p *Problem
	return errors.As(err, &p) && p.Slug() == slug
}

// WriteProblem writes p as the answer.
func WriteProblem(w http.ResponseWriter, p *Problem) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(p.Status)
	_ = json.NewEncoder(w).Encode(p) //nolint:errcheck // the status is sent; a failed body write means the client left
}

// WriteJSON writes v as a JSON answer with status.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v) //nolint:errcheck // the status is sent; a failed body write means the client left
}
