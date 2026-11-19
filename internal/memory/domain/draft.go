// SPDX-License-Identifier: MIT

package domain

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

// Limits, counted in characters. Tags are held to the record schema's 32
// characters, which is tighter than the write request's 40, so every stored
// record is a valid MemoryRecord.
const (
	maxTitle = 200
	maxBody  = 65536
	maxTags  = 32
)

var (
	tagPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)
	keyPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._/-]{0,127}$`)
)

// DraftInput is a write as the caller sent it, not yet checked.
type DraftInput struct {
	Scope string
	Key   string
	Title string
	Body  string
	Tags  []string
}

// Draft is a write that follows the rules.
type Draft struct {
	Scope Scope
	Key   string
	Title string
	Body  string
	Tags  []string
}

// NewDraft checks in against the memory rules. Duplicate tags are dropped,
// keeping the first of each.
func NewDraft(in DraftInput) (Draft, error) {
	scope, err := ParseScope(in.Scope)
	if err != nil {
		return Draft{}, err
	}
	if err := checkTitle(in.Title); err != nil {
		return Draft{}, err
	}
	if utf8.RuneCountInString(in.Body) > maxBody {
		return Draft{}, fmt.Errorf("%w: body is over %d characters", ErrTooLarge, maxBody)
	}
	if in.Key != "" && !keyPattern.MatchString(in.Key) {
		return Draft{}, fmt.Errorf("%w: key %q", ErrInvalid, in.Key)
	}
	tags, err := checkTags(in.Tags)
	if err != nil {
		return Draft{}, err
	}
	return Draft{Scope: scope, Key: in.Key, Title: in.Title, Body: in.Body, Tags: tags}, nil
}

func checkTitle(title string) error {
	n := utf8.RuneCountInString(title)
	if n == 0 || n > maxTitle {
		return fmt.Errorf("%w: title is %d characters, want 1 to %d", ErrInvalid, n, maxTitle)
	}
	if strings.ContainsAny(title, "\r\n") {
		return fmt.Errorf("%w: title has a line break", ErrInvalid)
	}
	return nil
}

func checkTags(in []string) ([]string, error) {
	tags := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, t := range in {
		if !tagPattern.MatchString(t) {
			return nil, fmt.Errorf("%w: tag %q", ErrInvalid, t)
		}
		if !seen[t] {
			seen[t] = true
			tags = append(tags, t)
		}
	}
	if len(tags) > maxTags {
		return nil, fmt.Errorf("%w: %d tags, at most %d", ErrInvalid, len(tags), maxTags)
	}
	return tags, nil
}
