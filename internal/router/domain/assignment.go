// SPDX-License-Identifier: MIT

package domain

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/SDK-E/agentsone/internal/core"
)

// MaxRequestText is the longest request, in characters, as the contract allows.
const MaxRequestText = 4000

// NewRequestText checks a human's request: trimmed, 1 to 4000 characters.
func NewRequestText(s string) (string, error) {
	s = strings.TrimSpace(s)
	switch n := utf8.RuneCountInString(s); {
	case n == 0:
		return "", fmt.Errorf("the request text is empty: %w", core.ErrInvalid)
	case n > MaxRequestText:
		return "", fmt.Errorf("the request text has %d characters, limit %d: %w", n, MaxRequestText, core.ErrInvalid)
	}
	return s, nil
}

// Assignment is the result of routing: who got the request and why.
type Assignment struct {
	ID            string
	Text          string
	Chosen        string
	Router        string // router that made the final pick
	Hops          []Hop
	Shortlist     []Shortlisted // best first; the chosen agent leads
	ChatPath      string
	ChatMessageID string
	CreatedBy     string
	CreatedAt     time.Time
}

// NewAssignment holds the facts of a routing result before it is checked.
type NewAssignment struct {
	ID, Text, Router, CreatedBy string
	Hops                        []Hop
	Shortlist                   []Shortlisted
	ChatPath, ChatMessageID     string
	Now                         time.Time
}

// BuildAssignment checks the facts and builds the assignment. The chosen
// agent is the first entry of the shortlist.
func BuildAssignment(n NewAssignment) (Assignment, error) {
	switch {
	case strings.TrimSpace(n.Text) == "":
		return Assignment{}, fmt.Errorf("assignment has no request text: %w", core.ErrInvalid)
	case len(n.Shortlist) == 0:
		return Assignment{}, fmt.Errorf("assignment has no shortlist: %w", core.ErrInvalid)
	case n.ID == "" || n.Router == "" || n.CreatedBy == "" || n.ChatPath == "" || n.ChatMessageID == "":
		return Assignment{}, fmt.Errorf("assignment misses an id, router, creator or chat: %w", core.ErrInvalid)
	}
	return Assignment{
		ID: n.ID, Text: n.Text, Chosen: n.Shortlist[0].Agent, Router: n.Router, Hops: n.Hops,
		Shortlist: n.Shortlist, ChatPath: n.ChatPath, ChatMessageID: n.ChatMessageID,
		CreatedBy: n.CreatedBy, CreatedAt: n.Now,
	}, nil
}

// AssignmentRepository keeps assignments so getAssignment can show them later.
type AssignmentRepository interface {
	Save(ctx context.Context, a Assignment) error
	// Get returns the assignment, or an error wrapping core.ErrNotFound.
	Get(ctx context.Context, id string) (Assignment, error)
}
