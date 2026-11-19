// SPDX-License-Identifier: MIT

package domain_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/memory/domain"
)

func TestParseScope(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in      string
		want    domain.Scope
		wantErr error
	}{
		{"private", domain.Private, nil},
		{"team", domain.Team, nil},
		{"common", domain.Common, nil},
		{"", "", domain.ErrInvalid},
		{"Private", "", domain.ErrInvalid},
		{"everyone", "", domain.ErrInvalid},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			t.Parallel()
			got, err := domain.ParseScope(tt.in)
			if !errors.Is(err, tt.wantErr) || got != tt.want {
				t.Fatalf("ParseScope(%q) = %q, %v; want %q, %v", tt.in, got, err, tt.want, tt.wantErr)
			}
		})
	}
}

func input() domain.DraftInput {
	return domain.DraftInput{Scope: "team", Title: "Site build steps", Body: "1. install", Tags: []string{"site", "howto"}}
}

func TestNewDraft_AcceptsAWellFormedWrite(t *testing.T) {
	t.Parallel()
	in := input()
	in.Key = "site/build"
	d, err := domain.NewDraft(in)
	if err != nil {
		t.Fatal(err)
	}
	if d.Scope != domain.Team || d.Key != "site/build" || d.Title != "Site build steps" || len(d.Tags) != 2 {
		t.Fatalf("draft %+v", d)
	}
}

func TestNewDraft_DropsDuplicateTagsKeepingOrder(t *testing.T) {
	t.Parallel()
	in := input()
	in.Tags = []string{"b", "a", "b"}
	d, err := domain.NewDraft(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Tags) != 2 || d.Tags[0] != "b" || d.Tags[1] != "a" {
		t.Fatalf("tags %v", d.Tags)
	}
}

func TestNewDraft_NoTagsIsAnEmptyList(t *testing.T) {
	t.Parallel()
	in := input()
	in.Tags = nil
	d, err := domain.NewDraft(in)
	if err != nil || d.Tags == nil || len(d.Tags) != 0 {
		t.Fatalf("draft %+v, err %v", d, err)
	}
}

func TestNewDraft_Refusals(t *testing.T) {
	t.Parallel()
	manyTags := make([]string, 33)
	for i := range manyTags {
		manyTags[i] = fmt.Sprintf("tag-%d", i)
	}
	tests := []struct {
		name    string
		mutate  func(*domain.DraftInput)
		wantErr error
	}{
		{"unknown scope", func(i *domain.DraftInput) { i.Scope = "world" }, domain.ErrInvalid},
		{"empty title", func(i *domain.DraftInput) { i.Title = "" }, domain.ErrInvalid},
		{"title with a line break", func(i *domain.DraftInput) { i.Title = "a\nb" }, domain.ErrInvalid},
		{"title of 200 characters", func(i *domain.DraftInput) { i.Title = strings.Repeat("x", 200) }, nil},
		{"title of 201 characters", func(i *domain.DraftInput) { i.Title = strings.Repeat("x", 201) }, domain.ErrInvalid},
		{"title of 200 multi-byte characters", func(i *domain.DraftInput) { i.Title = strings.Repeat("é", 200) }, nil},
		{"title of 201 multi-byte characters", func(i *domain.DraftInput) { i.Title = strings.Repeat("é", 201) }, domain.ErrInvalid},
		{"empty body", func(i *domain.DraftInput) { i.Body = "" }, nil},
		{"body of 65536 characters", func(i *domain.DraftInput) { i.Body = strings.Repeat("é", 65536) }, nil},
		{"body of 65537 characters", func(i *domain.DraftInput) { i.Body = strings.Repeat("é", 65537) }, domain.ErrTooLarge},
		{"tag with a capital", func(i *domain.DraftInput) { i.Tags = []string{"Site"} }, domain.ErrInvalid},
		{"tag starting with a hyphen", func(i *domain.DraftInput) { i.Tags = []string{"-a"} }, domain.ErrInvalid},
		{"tag of 32 characters", func(i *domain.DraftInput) { i.Tags = []string{strings.Repeat("a", 32)} }, nil},
		{"tag of 33 characters", func(i *domain.DraftInput) { i.Tags = []string{strings.Repeat("a", 33)} }, domain.ErrInvalid},
		{"32 distinct tags", func(i *domain.DraftInput) { i.Tags = manyTags[:32] }, nil},
		{"33 distinct tags", func(i *domain.DraftInput) { i.Tags = manyTags }, domain.ErrInvalid},
		{"key with a capital", func(i *domain.DraftInput) { i.Key = "Site" }, domain.ErrInvalid},
		{"key with a space", func(i *domain.DraftInput) { i.Key = "a b" }, domain.ErrInvalid},
		{"key of 128 characters", func(i *domain.DraftInput) { i.Key = strings.Repeat("k", 128) }, nil},
		{"key of 129 characters", func(i *domain.DraftInput) { i.Key = strings.Repeat("k", 129) }, domain.ErrInvalid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			in := input()
			tt.mutate(&in)
			_, err := domain.NewDraft(in)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("NewDraft() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestNewRecord_StartsAtRevisionOne(t *testing.T) {
	t.Parallel()
	d, _ := domain.NewDraft(input())
	now := time.Date(2026, 11, 19, 8, 5, 0, 0, time.UTC)

	r := domain.NewRecord("mem_1", d, "scribe", "builder", now)

	if r.Revision != 1 || !r.CreatedAt.Equal(now) || !r.UpdatedAt.Equal(now) {
		t.Fatalf("record %+v", r)
	}
	if r.Owner != "scribe" || r.Team != "builder" || r.Source.Kind != domain.SourceAgent || r.Source.Ref != "scribe" {
		t.Fatalf("record %+v", r)
	}
	if r.Links == nil || len(r.Links) != 0 {
		t.Fatalf("links %v", r.Links)
	}
}

func TestRecordReplace_KeepsIdentityAndRaisesRevision(t *testing.T) {
	t.Parallel()
	d, _ := domain.NewDraft(input())
	created := time.Date(2026, 11, 19, 8, 5, 0, 0, time.UTC)
	r := domain.NewRecord("mem_1", d, "scribe", "builder", created)
	in := input()
	in.Title = "New title"
	d2, _ := domain.NewDraft(in)
	later := created.Add(time.Hour)

	got := r.Replace(d2, "newhead", later)

	if got.ID != "mem_1" || !got.CreatedAt.Equal(created) || got.Owner != "scribe" {
		t.Fatalf("identity lost: %+v", got)
	}
	if got.Revision != 2 || !got.UpdatedAt.Equal(later) || got.Title != "New title" || got.Team != "newhead" {
		t.Fatalf("record %+v", got)
	}
}
