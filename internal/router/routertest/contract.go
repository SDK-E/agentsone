// SPDX-License-Identifier: MIT

package routertest

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/router/domain"
)

func mustLine(t *testing.T, s string) domain.Line {
	t.Helper()
	l, err := domain.NewLine(s)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

// RunLineContract runs the line repository contract against fresh
// repositories made by newRepo.
func RunLineContract(t *testing.T, newRepo func(t *testing.T) domain.LineRepository) {
	t.Helper()
	ctx := context.Background()
	at := time.Date(2026, 11, 19, 18, 39, 12, 123456789, time.UTC)

	t.Run("empty repository lists nothing", func(t *testing.T) {
		got, err := newRepo(t).List(ctx)
		if err != nil || len(got) != 0 {
			t.Fatalf("List = %v, %v", got, err)
		}
	})
	t.Run("lines round trip sorted by agent", func(t *testing.T) {
		r := newRepo(t)
		for _, a := range []string{"scribe", "builder"} {
			if err := r.Put(ctx, domain.StoredLine{Agent: a, Line: mustLine(t, "Goal: "+a), Updated: at}); err != nil {
				t.Fatal(err)
			}
		}
		got, err := r.List(ctx)
		if err != nil || len(got) != 2 || got[0].Agent != "builder" || got[1].Agent != "scribe" {
			t.Fatalf("List = %+v, %v", got, err)
		}
		if got[0].Line.String() != "Goal: builder" || !got[0].Updated.Equal(at) {
			t.Fatalf("line = %+v", got[0])
		}
	})
	t.Run("put replaces the previous line", func(t *testing.T) {
		r := newRepo(t)
		_ = r.Put(ctx, domain.StoredLine{Agent: "builder", Line: mustLine(t, "old"), Updated: at})
		_ = r.Put(ctx, domain.StoredLine{Agent: "builder", Line: mustLine(t, "new"), Updated: at.Add(time.Second)})
		got, _ := r.List(ctx)
		if len(got) != 1 || got[0].Line.String() != "new" {
			t.Fatalf("List = %+v", got)
		}
	})
	t.Run("delete forgets a line and ignores a missing one", func(t *testing.T) {
		r := newRepo(t)
		_ = r.Put(ctx, domain.StoredLine{Agent: "builder", Line: mustLine(t, "x"), Updated: at})
		if err := r.Delete(ctx, "builder"); err != nil {
			t.Fatal(err)
		}
		if err := r.Delete(ctx, "ghost"); err != nil {
			t.Fatalf("delete of a missing line: %v", err)
		}
		if got, _ := r.List(ctx); len(got) != 0 {
			t.Fatalf("List = %+v", got)
		}
	})
}

// RunAssignmentContract runs the assignment repository contract.
func RunAssignmentContract(t *testing.T, newRepo func(t *testing.T) domain.AssignmentRepository) {
	t.Helper()
	ctx := context.Background()
	want := domain.Assignment{
		ID: "asg_01JD7ZK3M00000000000000001", Text: "Add a page about budget walls", Chosen: "builder", Router: "docs-router",
		Hops: []domain.Hop{{Router: "router", HandedTo: "docs-router", Reason: "Docs field."}},
		Shortlist: []domain.Shortlisted{
			{Agent: "builder", Score: 0.91, Reason: "Owns the docs site.", OneLine: "Goal: ship the docs site."},
			{Agent: "scribe", Score: 0.78, Reason: "Writes copy.", OneLine: "Goal: clear docs."},
		},
		ChatPath: "/v1/agents/builder/chat", ChatMessageID: "cht_01JD7ZK3M00000000000000001",
		CreatedBy: "human:ada", CreatedAt: time.Date(2026, 11, 19, 18, 20, 2, 5, time.UTC),
	}

	t.Run("assignment round trips", func(t *testing.T) {
		r := newRepo(t)
		if err := r.Save(ctx, want); err != nil {
			t.Fatal(err)
		}
		got, err := r.Get(ctx, want.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !got.CreatedAt.Equal(want.CreatedAt) {
			t.Fatalf("created %v, want %v", got.CreatedAt, want.CreatedAt)
		}
		got.CreatedAt = want.CreatedAt
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %+v\nwant %+v", got, want)
		}
	})
	t.Run("assignment without hops round trips", func(t *testing.T) {
		r := newRepo(t)
		a := want
		a.Hops = nil
		_ = r.Save(ctx, a)
		got, err := r.Get(ctx, a.ID)
		if err != nil || len(got.Hops) != 0 {
			t.Fatalf("got %+v, %v", got, err)
		}
	})
	t.Run("unknown assignment is not found", func(t *testing.T) {
		if _, err := newRepo(t).Get(ctx, "asg_missing"); !errors.Is(err, core.ErrNotFound) {
			t.Fatalf("error = %v", err)
		}
	})
}
