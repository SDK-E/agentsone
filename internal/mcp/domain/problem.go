// SPDX-License-Identifier: MIT

package domain

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"
)

// Problem is an RFC 9457 problem detail as the daemon returns it.
type Problem struct {
	Type   string `json:"type"`
	Title  string `json:"title"`
	Status int    `json:"status"`
	Detail string `json:"detail"`
}

const maxPlainDetail = 200

// ParseProblem reads a problem+json body. A body that is not a problem
// becomes one built from the HTTP status, so callers always get a Problem.
func ParseProblem(status int, body []byte) Problem {
	var p Problem
	if err := json.Unmarshal(body, &p); err == nil && p.Type != "" {
		if p.Status == 0 {
			p.Status = status
		}
		return p
	}
	detail := strings.TrimSpace(string(body))
	if utf8.RuneCountInString(detail) > maxPlainDetail {
		detail = string([]rune(detail)[:maxPlainDetail]) + "..."
	}
	return Problem{Type: "about:blank", Title: http.StatusText(status), Status: status, Detail: detail}
}

// Error writes the problem in one plain line.
func (p Problem) Error() string {
	text := p.Title
	if p.Detail != "" {
		text += ": " + p.Detail
	}
	return fmt.Sprintf("%s (%s, status %d)", text, p.Type, p.Status)
}
