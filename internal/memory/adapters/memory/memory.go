// SPDX-License-Identifier: MIT

// Package memory is an in-memory memory repository for tests. It keeps the
// rules of the SQLite one (visibility, filters, ordering) with a simple word
// match in place of full-text search.
package memory

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"unicode"

	"github.com/SDK-E/agentsone/internal/memory/domain"
)

const (
	titleWeight = 10
	tagWeight   = 5
	snippetMax  = 240
	snippetLead = 60
)

// Repository holds records in a map.
type Repository struct {
	mu      sync.Mutex
	records map[string]domain.Record
}

var _ domain.Repository = (*Repository)(nil)

// New returns an empty repository.
func New() *Repository { return &Repository{records: map[string]domain.Record{}} }

// Get returns the record, or domain.ErrNotFound.
func (r *Repository) Get(_ context.Context, id string) (domain.Record, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec, ok := r.records[id]
	if !ok {
		return domain.Record{}, fmt.Errorf("memory %s: %w", id, domain.ErrNotFound)
	}
	return rec, nil
}

// FindByKey returns the record the owner wrote with key in scope.
func (r *Repository) FindByKey(_ context.Context, owner string, scope domain.Scope, key string) (domain.Record, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, rec := range r.records {
		if key != "" && rec.Key == key && rec.Owner == owner && rec.Scope == scope {
			return rec, true, nil
		}
	}
	return domain.Record{}, false, nil
}

// Save inserts rec or replaces the record with its id.
func (r *Repository) Save(_ context.Context, rec domain.Record) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.records[rec.ID] = rec
	return nil
}

// Delete removes the record, or returns domain.ErrNotFound.
func (r *Repository) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.records[id]; !ok {
		return fmt.Errorf("memory %s: %w", id, domain.ErrNotFound)
	}
	delete(r.records, id)
	return nil
}

// List returns visible records, newest first.
func (r *Repository) List(_ context.Context, q domain.ListQuery) (domain.Page, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []domain.Record
	for _, rec := range r.records {
		if !q.Visibility.Allows(rec) || !matchesFilters(rec, q) {
			continue
		}
		if q.After != "" && rec.ID >= q.After {
			continue
		}
		out = append(out, rec)
	}
	slices.SortFunc(out, func(a, b domain.Record) int { return strings.Compare(b.ID, a.ID) })
	more := len(out) > q.Limit
	if more {
		out = out[:q.Limit]
	}
	return domain.Page{Items: out, More: more}, nil
}

func matchesFilters(rec domain.Record, q domain.ListQuery) bool {
	return (q.Scope == "" || rec.Scope == q.Scope) &&
		(q.Owner == "" || rec.Owner == q.Owner) &&
		(q.Tag == "" || slices.Contains(rec.Tags, q.Tag))
}

// Search returns visible records that hold every word of the query.
func (r *Repository) Search(_ context.Context, q domain.SearchQuery) (domain.HitPage, error) {
	words := words(q.Text)
	if len(words) == 0 {
		return domain.HitPage{Items: []domain.Hit{}}, nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	var hits []domain.Hit
	for _, rec := range r.records {
		if !q.Visibility.Allows(rec) || (q.Scope != "" && rec.Scope != q.Scope) {
			continue
		}
		if score, ok := score(rec, words); ok {
			hits = append(hits, domain.Hit{Record: rec, Score: score, Snippet: snippet(rec, words)})
		}
	}
	slices.SortFunc(hits, func(a, b domain.Hit) int {
		if a.Score != b.Score {
			if a.Score > b.Score {
				return -1
			}
			return 1
		}
		return strings.Compare(b.Record.ID, a.Record.ID)
	})
	return pageOfHits(hits, q.Offset, q.Limit), nil
}

func pageOfHits(hits []domain.Hit, offset, limit int) domain.HitPage {
	if offset > len(hits) {
		offset = len(hits)
	}
	hits = hits[offset:]
	more := len(hits) > limit
	if more {
		hits = hits[:limit]
	}
	return domain.HitPage{Items: append([]domain.Hit{}, hits...), More: more}
}

// score is zero-free: ok is false unless every word occurs somewhere.
func score(rec domain.Record, ws []string) (float64, bool) {
	title, body := words(rec.Title), words(rec.Body)
	var tags []string
	for _, t := range rec.Tags {
		tags = append(tags, words(t)...)
	}
	total := 0.0
	for _, w := range ws {
		n := titleWeight*count(title, w) + tagWeight*count(tags, w) + count(body, w)
		if n == 0 {
			return 0, false
		}
		total += float64(n)
	}
	return total, true
}

func count(haystack []string, w string) int {
	n := 0
	for _, h := range haystack {
		if h == w {
			n++
		}
	}
	return n
}

// words splits text into lower-case words of letters and digits.
func words(text string) []string {
	return strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}

// snippet cuts a window of the body (or the title, when the body has no hit)
// and wraps every query word in brackets.
func snippet(rec domain.Record, ws []string) string {
	text := rec.Body
	if !hasWord(text, ws) {
		text = rec.Title
	}
	runes := []rune(text)
	var out strings.Builder
	first := -1
	for i := 0; i < len(runes); {
		if !isWordRune(runes[i]) {
			out.WriteRune(runes[i])
			i++
			continue
		}
		j := i
		for j < len(runes) && isWordRune(runes[j]) {
			j++
		}
		word := string(runes[i:j])
		if slices.Contains(ws, strings.ToLower(word)) {
			if first < 0 {
				first = len([]rune(out.String()))
			}
			out.WriteString("[" + word + "]")
		} else {
			out.WriteString(word)
		}
		i = j
	}
	return window(out.String(), first)
}

func window(s string, first int) string {
	runes := []rune(s)
	start := 0
	if first > snippetLead {
		start = first - snippetLead
	}
	end := min(len(runes), start+snippetMax)
	return string(runes[start:end])
}

func hasWord(text string, ws []string) bool {
	for _, w := range words(text) {
		if slices.Contains(ws, w) {
			return true
		}
	}
	return false
}

func isWordRune(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }
