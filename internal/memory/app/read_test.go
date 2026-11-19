// SPDX-License-Identifier: MIT

package app_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/memory/app"
)

// seed writes one record of each scope by scribe (team builder) and keeper.
func seed(f *fixture, t *testing.T) {
	t.Helper()
	f.write(t, "scribe", "private", "scribe private")
	f.write(t, "scribe", "team", "scribe team")
	f.write(t, "scribe", "common", "scribe common")
	f.write(t, "keeper", "private", "keeper private")
	f.write(t, "keeper", "team", "keeper team")
}

var human = core.Caller{Kind: core.CallerHuman, Name: "sam", UID: -1}

func TestList_ShowsEachReaderOnlyWhatItMayRead(t *testing.T) {
	tests := []struct {
		name string
		ctx  context.Context
		want []string // newest first
	}{
		{"scribe", agent("scribe"), []string{"scribe common", "scribe team", "scribe private"}},
		{"builder, scribe's team mate", agent("builder"), []string{"scribe common", "scribe team"}},
		{"keeper", agent("keeper"), []string{"keeper team", "keeper private", "scribe common"}},
		{"a root agent", agent("founder"), []string{"keeper team", "keeper private", "scribe common", "scribe team", "scribe private"}},
		{"a human", caller(human), []string{"keeper team", "keeper private", "scribe common", "scribe team", "scribe private"}},
		{"a plugin", caller(core.Caller{Kind: core.CallerPlugin, Name: "github", UID: -1}), []string{"scribe common"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			seed(f, t)

			page, err := f.svc.List(tt.ctx, app.ListInput{})
			if err != nil {
				t.Fatal(err)
			}

			if got := titles(page.Items); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("list = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestList_FiltersByScopeOwnerAndTag(t *testing.T) {
	f := newFixture(t)
	seed(f, t)
	if _, err := f.svc.Write(agent("scribe"), app.WriteInput{Scope: "common", Title: "tagged", Tags: []string{"report"}}); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		in   app.ListInput
		want []string
	}{
		{"scope", app.ListInput{Scope: "team"}, []string{"keeper team", "scribe team"}},
		{"owner", app.ListInput{Owner: "keeper"}, []string{"keeper team", "keeper private"}},
		{"tag", app.ListInput{Tag: "report"}, []string{"tagged"}},
	}
	for _, tt := range tests {
		page, err := f.svc.List(agent("founder"), tt.in)
		if err != nil {
			t.Fatal(err)
		}
		if got := titles(page.Items); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%s: list = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestList_PagesWithOpaqueCursors(t *testing.T) {
	f := newFixture(t)
	for _, title := range []string{"a", "b", "c", "d", "e"} {
		f.write(t, "scribe", "common", title)
	}

	var seen []string
	cursor := ""
	for pages := 0; ; pages++ {
		page, err := f.svc.List(agent("scribe"), app.ListInput{Limit: 2, Cursor: cursor})
		if err != nil {
			t.Fatal(err)
		}
		seen = append(seen, titles(page.Items)...)
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
		if pages > 5 {
			t.Fatal("paging does not end")
		}
	}

	if want := []string{"e", "d", "c", "b", "a"}; !reflect.DeepEqual(seen, want) {
		t.Fatalf("seen %v, want %v", seen, want)
	}
}

func TestList_RefusesBadInput(t *testing.T) {
	tests := []struct {
		name string
		in   app.ListInput
	}{
		{"a cursor that is not ours", app.ListInput{Cursor: "!!!"}},
		{"a cursor of the wrong kind", app.ListInput{Cursor: "b2Zmc2V0OjE"}},
		{"a negative limit", app.ListInput{Limit: -1}},
		{"a limit above 200", app.ListInput{Limit: 201}},
		{"an unknown scope", app.ListInput{Scope: "world"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			if _, err := f.svc.List(agent("scribe"), tt.in); !errors.Is(err, core.ErrInvalid) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestList_NeedsASignedInCaller(t *testing.T) {
	f := newFixture(t)
	if _, err := f.svc.List(context.Background(), app.ListInput{}); !errors.Is(err, core.ErrUnauthorized) {
		t.Fatalf("error = %v", err)
	}
}

func TestGet_ReadsWhatTheCallerMayRead(t *testing.T) {
	f := newFixture(t)
	private := f.write(t, "scribe", "private", "p")
	team := f.write(t, "scribe", "team", "t")
	tests := []struct {
		name    string
		ctx     context.Context
		id      string
		wantErr error
	}{
		{"owner reads private", agent("scribe"), private.ID, nil},
		{"team mate reads private", agent("builder"), private.ID, core.ErrNotFound},
		{"outsider reads team", agent("keeper"), team.ID, core.ErrNotFound},
		{"team mate reads team", agent("builder"), team.ID, nil},
		{"root agent reads private", agent("founder"), private.ID, nil},
		{"human reads private", caller(human), private.ID, nil},
		{"unknown id", agent("scribe"), "mem_missing", core.ErrNotFound},
		{"nobody signed in", context.Background(), private.ID, core.ErrUnauthorized},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := f.svc.Get(tt.ctx, tt.id)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestGet_AHiddenRecordLooksLikeAMissingOne(t *testing.T) {
	f := newFixture(t)
	hidden := f.write(t, "scribe", "private", "p")

	_, errHidden := f.svc.Get(agent("keeper"), hidden.ID)
	_, errMissing := f.svc.Get(agent("keeper"), "mem_missing")

	if strings.ReplaceAll(errHidden.Error(), hidden.ID, "X") != strings.ReplaceAll(errMissing.Error(), "mem_missing", "X") {
		t.Fatalf("%q differs from %q", errHidden, errMissing)
	}
}

func TestDelete_OwnersAndRootAgentsDelete(t *testing.T) {
	tests := []struct {
		name       string
		ctx        context.Context
		scope      string
		wantErr    error
		wantExists bool
	}{
		{"owner deletes", agent("scribe"), "common", nil, false},
		{"root agent deletes another's", agent("founder"), "private", nil, false},
		{"team mate cannot delete a team record", agent("builder"), "team", core.ErrForbidden, true},
		{"outsider cannot delete a common record", agent("keeper"), "common", core.ErrForbidden, true},
		{"outsider cannot see a private record", agent("keeper"), "private", core.ErrNotFound, true},
		{"human without root cannot delete", caller(human), "common", core.ErrForbidden, true},
		{"nobody signed in", context.Background(), "common", core.ErrUnauthorized, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			rec := f.write(t, "scribe", tt.scope, "r")

			err := f.svc.Delete(tt.ctx, rec.ID)

			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("error = %v, want %v", err, tt.wantErr)
			}
			_, getErr := f.svc.Get(agent("founder"), rec.ID)
			if exists := getErr == nil; exists != tt.wantExists {
				t.Fatalf("record exists = %v, want %v", exists, tt.wantExists)
			}
		})
	}
}

func TestDelete_ARootHumanDeletes(t *testing.T) {
	f := newFixture(t)
	rec := f.write(t, "scribe", "common", "r")
	root := human
	root.IsRoot = true

	if err := f.svc.Delete(caller(root), rec.ID); err != nil {
		t.Fatal(err)
	}
}

func TestDelete_UnknownIDIsNotFound(t *testing.T) {
	f := newFixture(t)
	if err := f.svc.Delete(agent("founder"), "mem_missing"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("error = %v", err)
	}
}
