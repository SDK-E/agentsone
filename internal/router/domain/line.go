// SPDX-License-Identifier: MIT

package domain

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/SDK-E/agentsone/internal/core"
)

// ErrInvalidLine is returned for a live line that is empty or too long.
var ErrInvalidLine = fmt.Errorf("invalid live line: %w", core.ErrInvalid)

// Line is one agent's one-line: its goal and what it does now. It is one
// line of clean text, at most core.MaxLiveLine bytes.
type Line struct{ text string }

// NewLine cleans raw into a Line. Control characters and line breaks become
// spaces, runs of spaces collapse and bad UTF-8 is dropped, so no runner can
// put a line break or a control code into the live list.
func NewLine(raw string) (Line, error) {
	clean := strings.ToValidUTF8(raw, "")
	clean = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return ' '
		}
		return r
	}, clean)
	clean = strings.Join(strings.Fields(clean), " ")
	switch {
	case clean == "":
		return Line{}, fmt.Errorf("line is empty: %w", ErrInvalidLine)
	case len(clean) > core.MaxLiveLine:
		return Line{}, fmt.Errorf("line has %d bytes, limit %d: %w", len(clean), core.MaxLiveLine, ErrInvalidLine)
	}
	return Line{text: clean}, nil
}

// String returns the line text.
func (l Line) String() string { return l.text }
