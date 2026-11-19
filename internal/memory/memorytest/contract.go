// SPDX-License-Identifier: MIT

// Package memorytest holds the memory repository contract: one suite every
// domain.Repository implementation must pass, in memory and on SQLite.
package memorytest

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/memory/domain"
)

// RunRepositoryContract runs the repository contract against fresh
// repositories made by newRepo.
func RunRepositoryContract(t *testing.T, newRepo func(t *testing.T) domain.Repository) {
	t.Helper()
	cases := []struct {
		name string
		run  func(t *testing.T, repo domain.Repository)
	}{
		{"unknown id is not found", unknownID},
		{"records round trip", roundTrip},
		{"save replaces a record with the same id", saveReplaces},
		{"find by key is per owner and scope", findByKey},
		{"delete removes a record", deleteRemoves},
		{"delete of an unknown id is not found", deleteUnknown},
		{"list is newest first and pages", listPages},
		{"list shows only visible records", listVisibility},
		{"list filters by scope, owner and tag", listFilters},
		{"search finds words in title, body and tags", searchFields},
		{"search ranks a title hit above a body hit", searchRanking},
		{"search marks the hit in the snippet", searchSnippet},
		{"search shows only visible records", searchVisibility},
		{"search filters by scope", searchScope},
		{"search pages", searchPages},
		{"search survives query syntax", searchSyntax},
		{"search follows saves and deletes", searchFollowsChanges},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { c.run(t, newRepo(t)) })
	}
}

var ctx = context.Background()

// Rec builds a record. The id sorts by creation: mem_01 is older than mem_02.
func Rec(n int, scope domain.Scope, owner, team, title, body string, tags ...string) domain.Record {
	if tags == nil {
		tags = []string{}
	}
	at := time.Date(2026, 11, 19, 8, n, 0, 123456789, time.UTC)
	return domain.Record{
		ID: fmt.Sprintf("mem_%02d", n), Scope: scope, Owner: owner, Team: team,
		Title: title, Body: body, Tags: tags, Links: []domain.Link{},
		Source:    domain.Source{Kind: domain.SourceAgent, Ref: owner},
		CreatedAt: at, UpdatedAt: at, Revision: 1,
	}
}

func save(t *testing.T, repo domain.Repository, records ...domain.Record) {
	t.Helper()
	for _, r := range records {
		if err := repo.Save(ctx, r); err != nil {
			t.Fatalf("save %s: %v", r.ID, err)
		}
	}
}

func ids(records []domain.Record) []string {
	out := make([]string, 0, len(records))
	for _, r := range records {
		out = append(out, r.ID)
	}
	return out
}

func hitIDs(hits []domain.Hit) []string {
	out := make([]string, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.Record.ID)
	}
	return out
}

func expect(t *testing.T, what string, got, want []string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s = %v, want %v", what, got, want)
	}
}

var all = domain.Visibility{All: true}

func unknownID(t *testing.T, repo domain.Repository) {
	if _, err := repo.Get(ctx, "mem_missing"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("error = %v, want not found", err)
	}
}

func roundTrip(t *testing.T, repo domain.Repository) {
	r := Rec(1, domain.Team, "scribe", "builder", "Site build steps", "1. `pnpm install`\n2. build", "site", "howto")
	r.Key = "site/build"
	r.Links = []domain.Link{{Kind: "task", Ref: "tsk_1"}, {Kind: "url", Ref: "https://example.com", Title: "Home"}}
	r.Source = domain.Source{Kind: domain.SourceEvent, Ref: "evt_1"}
	r.Revision = 3
	save(t, repo, r)

	got, err := repo.Get(ctx, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, r) {
		t.Fatalf("got  %+v\nwant %+v", got, r)
	}
}

func saveReplaces(t *testing.T, repo domain.Repository) {
	r := Rec(1, domain.Common, "scribe", "builder", "Old title", "old", "a")
	save(t, repo, r)
	r.Title, r.Tags, r.Revision = "New title", []string{"b"}, 2
	save(t, repo, r)

	got, _ := repo.Get(ctx, r.ID)
	if got.Title != "New title" || got.Revision != 2 || !reflect.DeepEqual(got.Tags, []string{"b"}) {
		t.Fatalf("got %+v", got)
	}
	page, _ := repo.List(ctx, domain.ListQuery{Visibility: all, Limit: 10})
	expect(t, "list", ids(page.Items), []string{"mem_01"})
	byOldTag, _ := repo.List(ctx, domain.ListQuery{Visibility: all, Tag: "a", Limit: 10})
	if len(byOldTag.Items) != 0 {
		t.Fatalf("old tag still finds %v", ids(byOldTag.Items))
	}
}

func findByKey(t *testing.T, repo domain.Repository) {
	a := Rec(1, domain.Common, "scribe", "builder", "A", "")
	a.Key = "notes"
	b := Rec(2, domain.Common, "keeper", "keeper", "B", "")
	b.Key = "notes"
	save(t, repo, a, b)

	got, ok, err := repo.FindByKey(ctx, "scribe", domain.Common, "notes")
	if err != nil || !ok || got.ID != "mem_01" {
		t.Fatalf("got %v %v %v", got.ID, ok, err)
	}
	if _, ok, _ := repo.FindByKey(ctx, "scribe", domain.Private, "notes"); ok {
		t.Fatal("found the key in another scope")
	}
	if _, ok, _ := repo.FindByKey(ctx, "builder", domain.Common, "notes"); ok {
		t.Fatal("found another owner's key")
	}
}

func deleteRemoves(t *testing.T, repo domain.Repository) {
	save(t, repo, Rec(1, domain.Common, "scribe", "builder", "Disk", "disk notes", "x"))
	if err := repo.Delete(ctx, "mem_01"); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Get(ctx, "mem_01"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("get after delete: %v", err)
	}
	hits, _ := repo.Search(ctx, domain.SearchQuery{Visibility: all, Text: "disk", Limit: 10})
	if len(hits.Items) != 0 {
		t.Fatalf("search finds a deleted record: %v", hitIDs(hits.Items))
	}
}

func deleteUnknown(t *testing.T, repo domain.Repository) {
	if err := repo.Delete(ctx, "mem_missing"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("error = %v, want not found", err)
	}
}

func fiveCommon(t *testing.T, repo domain.Repository) {
	for n := 1; n <= 5; n++ {
		save(t, repo, Rec(n, domain.Common, "scribe", "builder", fmt.Sprintf("Note %d", n), ""))
	}
}

func listPages(t *testing.T, repo domain.Repository) {
	fiveCommon(t, repo)
	first, err := repo.List(ctx, domain.ListQuery{Visibility: all, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	expect(t, "first page", ids(first.Items), []string{"mem_05", "mem_04"})
	if !first.More {
		t.Fatal("first page says nothing follows")
	}
	second, _ := repo.List(ctx, domain.ListQuery{Visibility: all, Limit: 2, After: "mem_04"})
	expect(t, "second page", ids(second.Items), []string{"mem_03", "mem_02"})
	last, _ := repo.List(ctx, domain.ListQuery{Visibility: all, Limit: 2, After: "mem_02"})
	expect(t, "last page", ids(last.Items), []string{"mem_01"})
	if last.More {
		t.Fatal("last page says more follows")
	}
	exact, _ := repo.List(ctx, domain.ListQuery{Visibility: all, Limit: 5})
	if len(exact.Items) != 5 || exact.More {
		t.Fatalf("a page that holds everything: %d items, more %v", len(exact.Items), exact.More)
	}
}

func visibilityFixture(t *testing.T, repo domain.Repository) {
	save(t, repo,
		Rec(1, domain.Private, "scribe", "builder", "Private of scribe", "disk"),
		Rec(2, domain.Team, "scribe", "builder", "Team builder", "disk"),
		Rec(3, domain.Common, "scribe", "builder", "Common", "disk"),
		Rec(4, domain.Private, "keeper", "keeper", "Private of keeper", "disk"),
		Rec(5, domain.Team, "keeper", "keeper", "Team keeper", "disk"),
	)
}

var visibilityCases = []struct {
	name string
	vis  domain.Visibility
	want []string // newest first
}{
	{"owner and team of scribe", domain.Visibility{Owner: "scribe", Team: "builder"}, []string{"mem_03", "mem_02", "mem_01"}},
	{"keeper", domain.Visibility{Owner: "keeper", Team: "keeper"}, []string{"mem_05", "mem_04", "mem_03"}},
	{"team mate of scribe", domain.Visibility{Owner: "builder", Team: "builder"}, []string{"mem_03", "mem_02"}},
	{"agent outside every team", domain.Visibility{Owner: "x"}, []string{"mem_03"}},
	{"common only", domain.Visibility{}, []string{"mem_03"}},
	{"all", all, []string{"mem_05", "mem_04", "mem_03", "mem_02", "mem_01"}},
}

func listVisibility(t *testing.T, repo domain.Repository) {
	visibilityFixture(t, repo)
	for _, c := range visibilityCases {
		page, err := repo.List(ctx, domain.ListQuery{Visibility: c.vis, Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		expect(t, c.name, ids(page.Items), c.want)
	}
}

func listFilters(t *testing.T, repo domain.Repository) {
	save(t, repo,
		Rec(1, domain.Common, "scribe", "builder", "A", "", "report", "health"),
		Rec(2, domain.Team, "scribe", "builder", "B", "", "report"),
		Rec(3, domain.Common, "keeper", "keeper", "C", "", "disk"),
	)
	tests := []struct {
		name string
		q    domain.ListQuery
		want []string
	}{
		{"scope", domain.ListQuery{Scope: domain.Common}, []string{"mem_03", "mem_01"}},
		{"owner", domain.ListQuery{Owner: "scribe"}, []string{"mem_02", "mem_01"}},
		{"tag", domain.ListQuery{Tag: "report"}, []string{"mem_02", "mem_01"}},
		{"tag that is only part of another tag", domain.ListQuery{Tag: "heal"}, []string{}},
		{"scope, owner and tag", domain.ListQuery{Scope: domain.Common, Owner: "scribe", Tag: "report"}, []string{"mem_01"}},
	}
	for _, tt := range tests {
		tt.q.Visibility, tt.q.Limit = all, 10
		page, err := repo.List(ctx, tt.q)
		if err != nil {
			t.Fatal(err)
		}
		expect(t, tt.name, ids(page.Items), tt.want)
	}
}

func search(t *testing.T, repo domain.Repository, text string) []domain.Hit {
	t.Helper()
	page, err := repo.Search(ctx, domain.SearchQuery{Visibility: all, Text: text, Limit: 10})
	if err != nil {
		t.Fatalf("search %q: %v", text, err)
	}
	return page.Items
}

func searchFields(t *testing.T, repo domain.Repository) {
	save(t, repo,
		Rec(1, domain.Common, "a", "a", "Journal notes", "nothing here"),
		Rec(2, domain.Common, "a", "a", "Other", "vacuum the journal weekly"),
		Rec(3, domain.Common, "a", "a", "Third", "nothing", "journal"),
		Rec(4, domain.Common, "a", "a", "Unrelated", "text"),
	)
	got := hitIDs(search(t, repo, "journal"))
	if len(got) != 3 || got[0] == "mem_04" || got[1] == "mem_04" || got[2] == "mem_04" {
		t.Fatalf("hits %v", got)
	}
	expect(t, "case-insensitive", hitIDs(search(t, repo, "JOURNAL WEEKLY")), []string{"mem_02"})
	expect(t, "no match", hitIDs(search(t, repo, "zebra")), []string{})
}

func searchRanking(t *testing.T, repo domain.Repository) {
	save(t, repo,
		Rec(1, domain.Common, "a", "a", "Weekly report", "the disk was fine"),
		Rec(2, domain.Common, "a", "a", "Disk report", "all good"),
	)
	hits := search(t, repo, "disk")
	expect(t, "order", hitIDs(hits), []string{"mem_02", "mem_01"})
	if !(hits[0].Score > hits[1].Score) {
		t.Fatalf("scores %v then %v, want the first higher", hits[0].Score, hits[1].Score)
	}
}

func searchSnippet(t *testing.T, repo domain.Repository) {
	save(t, repo, Rec(1, domain.Common, "a", "a", "Weekly report", "Disk on /var reached 86 percent. Keeper cleaned old journal files."))
	hits := search(t, repo, "journal")
	if len(hits) != 1 || !strings.Contains(strings.ToLower(hits[0].Snippet), "[journal]") {
		t.Fatalf("hits %+v", hits)
	}
	if n := len([]rune(hits[0].Snippet)); n == 0 || n > 500 {
		t.Fatalf("snippet length %d", n)
	}
}

func searchVisibility(t *testing.T, repo domain.Repository) {
	visibilityFixture(t, repo)
	for _, c := range visibilityCases {
		page, err := repo.Search(ctx, domain.SearchQuery{Visibility: c.vis, Text: "disk", Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		got := hitIDs(page.Items)
		if !sameSet(got, c.want) {
			t.Fatalf("%s: hits %v, want %v", c.name, got, c.want)
		}
	}
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := map[string]bool{}
	for _, s := range a {
		seen[s] = true
	}
	for _, s := range b {
		if !seen[s] {
			return false
		}
	}
	return true
}

func searchScope(t *testing.T, repo domain.Repository) {
	visibilityFixture(t, repo)
	page, _ := repo.Search(ctx, domain.SearchQuery{Visibility: all, Text: "disk", Scope: domain.Team, Limit: 10})
	if got := hitIDs(page.Items); !sameSet(got, []string{"mem_02", "mem_05"}) {
		t.Fatalf("hits %v", got)
	}
}

func searchPages(t *testing.T, repo domain.Repository) {
	for n := 1; n <= 5; n++ {
		save(t, repo, Rec(n, domain.Common, "a", "a", fmt.Sprintf("Disk %d", n), "same body"))
	}
	var seen []string
	for offset := 0; ; offset += 2 {
		page, err := repo.Search(ctx, domain.SearchQuery{Visibility: all, Text: "disk", Offset: offset, Limit: 2})
		if err != nil {
			t.Fatal(err)
		}
		seen = append(seen, hitIDs(page.Items)...)
		if !page.More {
			break
		}
		if offset > 10 {
			t.Fatal("paging does not end")
		}
	}
	if !sameSet(seen, []string{"mem_01", "mem_02", "mem_03", "mem_04", "mem_05"}) || len(seen) != 5 {
		t.Fatalf("pages gave %v", seen)
	}
}

func searchSyntax(t *testing.T, repo domain.Repository) {
	save(t, repo, Rec(1, domain.Common, "a", "a", "Disk", "and or not near"))
	for _, q := range []string{`"`, `disk"`, `AND`, `disk OR`, `(disk`, `disk*`, `title:disk`, `-disk`, `^disk`, `a:b:c`, `!!!`, `   `} {
		if _, err := repo.Search(ctx, domain.SearchQuery{Visibility: all, Text: q, Limit: 10}); err != nil {
			t.Fatalf("search %q: %v", q, err)
		}
	}
	expect(t, "operator words are plain words", hitIDs(search(t, repo, "disk AND")), []string{"mem_01"})
}

func searchFollowsChanges(t *testing.T, repo domain.Repository) {
	r := Rec(1, domain.Common, "a", "a", "Old", "alpha")
	save(t, repo, r)
	r.Body = "beta"
	save(t, repo, r)
	expect(t, "old text", hitIDs(search(t, repo, "alpha")), []string{})
	expect(t, "new text", hitIDs(search(t, repo, "beta")), []string{"mem_01"})
}
