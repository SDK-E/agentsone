// SPDX-License-Identifier: MIT

package domain

import "time"

// SourceKind says where a memory's content came from.
type SourceKind string

// Source kinds, with the contract's values.
const (
	SourceAgent  SourceKind = "agent"
	SourceHuman  SourceKind = "human"
	SourceEvent  SourceKind = "event"
	SourceImport SourceKind = "import"
	SourcePlugin SourceKind = "plugin"
)

// Source is where a memory's content came from.
type Source struct {
	Kind SourceKind
	Ref  string
}

// Link points at something a memory is about.
type Link struct {
	Kind  string
	Ref   string
	Title string
}

// Record is one memory. Its team is the head of the owner's chart branch when
// it was written; that decides who reads a team memory.
type Record struct {
	ID        string
	Scope     Scope
	Owner     string
	Team      string
	Key       string // stable key for upserts, empty when none
	Title     string
	Body      string
	Tags      []string
	Links     []Link
	Source    Source
	CreatedAt time.Time
	UpdatedAt time.Time
	// Revision starts at 1 and rises by 1 on every change.
	Revision int
}

// NewRecord makes the first revision of a record from a checked draft.
func NewRecord(id string, d Draft, owner, team string, now time.Time) Record {
	return Record{
		ID: id, Scope: d.Scope, Owner: owner, Team: team, Key: d.Key,
		Title: d.Title, Body: d.Body, Tags: d.Tags, Links: []Link{},
		Source:    Source{Kind: SourceAgent, Ref: owner},
		CreatedAt: now.UTC(), UpdatedAt: now.UTC(), Revision: 1,
	}
}

// Replace returns r with the content of d. The id, owner, key and creation
// time stay; the team is the owner's current one.
func (r Record) Replace(d Draft, team string, now time.Time) Record {
	r.Team, r.Title, r.Body, r.Tags = team, d.Title, d.Body, d.Tags
	r.UpdatedAt = now.UTC()
	r.Revision++
	return r
}
