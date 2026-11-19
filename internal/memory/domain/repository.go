// SPDX-License-Identifier: MIT

package domain

import "context"

// ListQuery asks for readable records, newest first.
type ListQuery struct {
	Visibility Visibility
	// Scope, Owner and Tag narrow the list when not empty.
	Scope Scope
	Owner string
	Tag   string
	// After is the id of the last record of the previous page.
	After string
	Limit int
}

// Page is one page of records. More is true when records follow it.
type Page struct {
	Items []Record
	More  bool
}

// SearchQuery asks for readable records matching Text, best match first.
type SearchQuery struct {
	Visibility Visibility
	Text       string
	Scope      Scope
	// Offset is how many hits earlier pages held.
	Offset int
	Limit  int
}

// Hit is a record found by a search.
type Hit struct {
	Record Record
	// Score is the rank; higher is better.
	Score float64
	// Snippet is matching text with hits wrapped in "[" and "]".
	Snippet string
}

// HitPage is one page of hits. More is true when hits follow it.
type HitPage struct {
	Items []Hit
	More  bool
}

// Repository stores memory records. Every implementation passes the suite in
// internal/memory/memorytest.
type Repository interface {
	// Get returns the record, or ErrNotFound.
	Get(ctx context.Context, id string) (Record, error)
	// FindByKey returns the record the owner wrote with key in scope.
	FindByKey(ctx context.Context, owner string, scope Scope, key string) (Record, bool, error)
	// Save inserts r, or replaces the stored record with the same id.
	Save(ctx context.Context, r Record) error
	// Delete removes the record, or returns ErrNotFound.
	Delete(ctx context.Context, id string) error
	List(ctx context.Context, q ListQuery) (Page, error)
	Search(ctx context.Context, q SearchQuery) (HitPage, error)
}
