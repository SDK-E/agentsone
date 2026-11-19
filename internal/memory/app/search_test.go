// SPDX-License-Identifier: MIT

package app_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/memory/app"
	"github.com/SDK-E/agentsone/internal/memory/domain"
)

func hitTitles(hits []domain.Hit) []string {
	out := make([]string, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.Record.Title)
	}
	slices.Sort(out)
	return out
}

func TestSearch_FindsOnlyWhatTheCallerMayRead(t *testing.T) {
	f := newFixture(t)
	seed(f, t) // every body contains "body"
	tests := []struct {
		name string
		ctx  context.Context
		want []string
	}{
		{"scribe", agent("scribe"), []string{"scribe common", "scribe private", "scribe team"}},
		{"keeper", agent("keeper"), []string{"keeper private", "keeper team", "scribe common"}},
		{"root agent", agent("founder"), []string{"keeper private", "keeper team", "scribe common", "scribe private", "scribe team"}},
		{"builder, scribe's team mate", agent("builder"), []string{"scribe common", "scribe team"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			page, err := f.svc.Search(tt.ctx, app.SearchInput{Text: "body"})
			if err != nil {
				t.Fatal(err)
			}
			if got := hitTitles(page.Items); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("hits %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSearch_ReturnsTheRankAndSnippet(t *testing.T) {
	f := newFixture(t)
	f.write(t, "scribe", "common", "Disk report")

	page, err := f.svc.Search(agent("scribe"), app.SearchInput{Text: "disk"})

	if err != nil || len(page.Items) != 1 {
		t.Fatalf("page %+v, err %v", page, err)
	}
	if h := page.Items[0]; h.Score <= 0 || !strings.Contains(h.Snippet, "[Disk]") {
		t.Fatalf("hit %+v", h)
	}
}

func TestSearch_NarrowsToAScope(t *testing.T) {
	f := newFixture(t)
	seed(f, t)

	page, err := f.svc.Search(agent("founder"), app.SearchInput{Text: "body", Scope: "team"})

	if err != nil {
		t.Fatal(err)
	}
	if got := hitTitles(page.Items); !reflect.DeepEqual(got, []string{"keeper team", "scribe team"}) {
		t.Fatalf("hits %v", got)
	}
}

func TestSearch_PagesWithOpaqueCursors(t *testing.T) {
	f := newFixture(t)
	for i := range 5 {
		f.write(t, "scribe", "common", fmt.Sprintf("note %d", i))
	}
	var seen []string
	cursor := ""
	for pages := 0; ; pages++ {
		page, err := f.svc.Search(agent("scribe"), app.SearchInput{Text: "body", Limit: 2, Cursor: cursor})
		if err != nil {
			t.Fatal(err)
		}
		seen = append(seen, hitTitles(page.Items)...)
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
		if pages > 5 {
			t.Fatal("paging does not end")
		}
	}
	if len(seen) != 5 {
		t.Fatalf("seen %v", seen)
	}
}

func TestSearch_RefusesBadInput(t *testing.T) {
	tests := []struct {
		name string
		in   app.SearchInput
	}{
		{"empty text", app.SearchInput{}},
		{"blank text", app.SearchInput{Text: "   "}},
		{"text over 500 characters", app.SearchInput{Text: strings.Repeat("é", 501)}},
		{"a bad cursor", app.SearchInput{Text: "x", Cursor: "!!!"}},
		{"a cursor of the wrong kind", app.SearchInput{Text: "x", Cursor: "aWQ6bWVtXzE"}},
		{"a limit above 200", app.SearchInput{Text: "x", Limit: 201}},
		{"an unknown scope", app.SearchInput{Text: "x", Scope: "world"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			if _, err := f.svc.Search(agent("scribe"), tt.in); !errors.Is(err, core.ErrInvalid) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestSearch_AcceptsTextOf500Characters(t *testing.T) {
	f := newFixture(t)
	if _, err := f.svc.Search(agent("scribe"), app.SearchInput{Text: strings.Repeat("é", 500)}); err != nil {
		t.Fatal(err)
	}
}

func TestSearch_NeedsASignedInCaller(t *testing.T) {
	f := newFixture(t)
	if _, err := f.svc.Search(context.Background(), app.SearchInput{Text: "x"}); !errors.Is(err, core.ErrUnauthorized) {
		t.Fatalf("error = %v", err)
	}
}
