// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/memory/domain"
)

const (
	defaultLimit = 50
	maxLimit     = 200
	maxQueryText = 500

	idCursor     = "id:"
	offsetCursor = "offset:"
)

// ListInput narrows a list. Empty fields do not narrow.
type ListInput struct {
	Scope  string
	Owner  string
	Tag    string
	Cursor string
	Limit  int // 0 means the default of 50
}

// RecordPage is one page of a list. NextCursor is empty on the last page.
type RecordPage struct {
	Items      []domain.Record
	NextCursor string
}

// List returns the records the caller may read, newest first.
func (s *Service) List(ctx context.Context, in ListInput) (RecordPage, error) {
	p, err := s.principal(ctx)
	if err != nil {
		return RecordPage{}, err
	}
	q := domain.ListQuery{Visibility: domain.VisibilityOf(p), Owner: in.Owner, Tag: in.Tag}
	if q.Scope, err = optionalScope(in.Scope); err != nil {
		return RecordPage{}, err
	}
	if q.Limit, err = pageSize(in.Limit); err != nil {
		return RecordPage{}, err
	}
	if q.After, err = decodeCursor(in.Cursor, idCursor); err != nil {
		return RecordPage{}, err
	}
	page, err := s.repo.List(ctx, q)
	if err != nil {
		return RecordPage{}, fmt.Errorf("list memory: %w", err)
	}
	out := RecordPage{Items: page.Items}
	if page.More {
		out.NextCursor = encodeCursor(idCursor, page.Items[len(page.Items)-1].ID)
	}
	return out, nil
}

// SearchInput is a full-text search. Text is words that must all occur.
type SearchInput struct {
	Text   string
	Scope  string
	Cursor string
	Limit  int // 0 means the default of 50
}

// HitPage is one page of search hits. NextCursor is empty on the last page.
type HitPage struct {
	Items      []domain.Hit
	NextCursor string
}

// Search returns the records the caller may read that match, best match first.
func (s *Service) Search(ctx context.Context, in SearchInput) (HitPage, error) {
	p, err := s.principal(ctx)
	if err != nil {
		return HitPage{}, err
	}
	q := domain.SearchQuery{Visibility: domain.VisibilityOf(p), Text: in.Text}
	if n := utf8.RuneCountInString(strings.TrimSpace(in.Text)); n == 0 || n > maxQueryText {
		return HitPage{}, fmt.Errorf("search text is %d characters, want 1 to %d: %w", n, maxQueryText, core.ErrInvalid)
	}
	if q.Scope, err = optionalScope(in.Scope); err != nil {
		return HitPage{}, err
	}
	if q.Limit, err = pageSize(in.Limit); err != nil {
		return HitPage{}, err
	}
	offset, err := decodeCursor(in.Cursor, offsetCursor)
	if err != nil {
		return HitPage{}, err
	}
	if offset != "" {
		if q.Offset, err = strconv.Atoi(offset); err != nil || q.Offset < 0 {
			return HitPage{}, fmt.Errorf("cursor: %w", core.ErrInvalid)
		}
	}
	page, err := s.repo.Search(ctx, q)
	if err != nil {
		return HitPage{}, fmt.Errorf("search memory: %w", err)
	}
	out := HitPage{Items: page.Items}
	if page.More {
		out.NextCursor = encodeCursor(offsetCursor, strconv.Itoa(q.Offset+len(page.Items)))
	}
	return out, nil
}

func optionalScope(s string) (domain.Scope, error) {
	if s == "" {
		return "", nil
	}
	scope, err := domain.ParseScope(s)
	return scope, translate(err)
}

func pageSize(n int) (int, error) {
	switch {
	case n == 0:
		return defaultLimit, nil
	case n < 0 || n > maxLimit:
		return 0, fmt.Errorf("limit %d, want 1 to %d: %w", n, maxLimit, core.ErrInvalid)
	default:
		return n, nil
	}
}

// Cursors are base64 text of "<kind>:<value>", so a cursor from a list is not
// taken for one from a search.
func encodeCursor(kind, value string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(kind + value))
}

func decodeCursor(cursor, kind string) (string, error) {
	if cursor == "" {
		return "", nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	value, ok := strings.CutPrefix(string(raw), kind)
	if err != nil || !ok || value == "" {
		return "", fmt.Errorf("cursor is not valid: %w", core.ErrInvalid)
	}
	return value, nil
}
