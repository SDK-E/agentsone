// SPDX-License-Identifier: MIT

package httpadapter

import (
	"github.com/SDK-E/agentsone/internal/contract/api"
	"github.com/SDK-E/agentsone/internal/memory/app"
	"github.com/SDK-E/agentsone/internal/memory/domain"
)

// The generated types appear only in this file and in handler.go, so a
// contract rename stays a change to the adapter alone.

func toWriteInput(w api.MemoryWrite) app.WriteInput {
	in := app.WriteInput{Scope: string(w.Scope), Title: w.Title, Body: w.Body}
	if w.Key != nil {
		in.Key = *w.Key
	}
	if w.Tags != nil {
		in.Tags = *w.Tags
	}
	return in
}

func toMemoryRecord(r domain.Record) api.MemoryRecord {
	links := make([]api.Link, 0, len(r.Links))
	for _, l := range r.Links {
		links = append(links, api.Link{Kind: api.LinkKind(l.Kind), Ref: l.Ref, Title: optional(l.Title)})
	}
	tags := r.Tags
	if tags == nil {
		tags = []string{}
	}
	return api.MemoryRecord{
		Id: r.ID, Scope: api.MemoryRecordScope(r.Scope), Owner: r.Owner, Team: r.Team,
		Title: r.Title, Body: r.Body, Tags: tags, Links: links,
		Source:    api.MemorySource{Kind: api.MemorySourceKind(r.Source.Kind), Ref: optional(r.Source.Ref)},
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt, Revision: r.Revision,
	}
}

func toMemoryList(p app.RecordPage) api.MemoryList {
	items := make([]api.MemoryRecord, 0, len(p.Items))
	for _, r := range p.Items {
		items = append(items, toMemoryRecord(r))
	}
	return api.MemoryList{Items: items, NextCursor: optional(p.NextCursor)}
}

func toSearchResult(p app.HitPage) api.MemorySearchResult {
	items := make([]api.MemoryHit, 0, len(p.Items))
	for _, h := range p.Items {
		items = append(items, api.MemoryHit{Record: toMemoryRecord(h.Record), Score: float32(h.Score), Snippet: h.Snippet})
	}
	return api.MemorySearchResult{Items: items, NextCursor: optional(p.NextCursor)}
}

// optional is nil for an empty string, which the contract writes as an absent
// field or null.
func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func valueOr[T any](p *T, fallback T) T {
	if p == nil {
		return fallback
	}
	return *p
}
