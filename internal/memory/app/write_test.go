// SPDX-License-Identifier: MIT

package app_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/core/coretest"
	"github.com/SDK-E/agentsone/internal/memory/app"
	"github.com/SDK-E/agentsone/internal/memory/domain"
)

func TestWrite_StoresARecordOwnedByTheCaller(t *testing.T) {
	f := newFixture(t)

	rec, err := f.svc.Write(agent("scribe"), app.WriteInput{
		Scope: "team", Title: "Site build steps", Body: "1. install", Tags: []string{"site"},
	})
	if err != nil {
		t.Fatal(err)
	}

	if !strings.HasPrefix(rec.ID, "mem_") || rec.Owner != "scribe" || rec.Team != "builder" {
		t.Fatalf("record %+v", rec)
	}
	if rec.Scope != domain.Team || rec.Revision != 1 || !rec.CreatedAt.Equal(coretest.Epoch) {
		t.Fatalf("record %+v", rec)
	}
	if rec.Source.Kind != domain.SourceAgent || rec.Source.Ref != "scribe" {
		t.Fatalf("source %+v", rec.Source)
	}
	got, err := f.svc.Get(agent("scribe"), rec.ID)
	if err != nil || got.Title != "Site build steps" {
		t.Fatalf("read back %+v, %v", got, err)
	}
}

func TestWrite_ARootAgentWritesToo(t *testing.T) {
	f := newFixture(t)
	rec := f.write(t, "founder", "common", "Charter")
	if rec.Owner != "founder" || rec.Team != "founder" {
		t.Fatalf("record %+v", rec)
	}
}

func TestWrite_SameKeyReplacesTheOwnersRecord(t *testing.T) {
	f := newFixture(t)
	first, _ := f.svc.Write(agent("scribe"), app.WriteInput{Scope: "common", Key: "notes", Title: "v1", Body: "one"})
	f.fakes.Clock.Advance(time.Hour)

	second, err := f.svc.Write(agent("scribe"), app.WriteInput{Scope: "common", Key: "notes", Title: "v2", Body: "two"})
	if err != nil {
		t.Fatal(err)
	}

	if second.ID != first.ID || second.Revision != 2 || second.Title != "v2" {
		t.Fatalf("second %+v", second)
	}
	if !second.CreatedAt.Equal(first.CreatedAt) || !second.UpdatedAt.Equal(first.CreatedAt.Add(time.Hour)) {
		t.Fatalf("times %v %v", second.CreatedAt, second.UpdatedAt)
	}
	page, _ := f.svc.List(agent("scribe"), app.ListInput{})
	if len(page.Items) != 1 {
		t.Fatalf("list %v", titles(page.Items))
	}
}

func TestWrite_SameKeyOfAnotherOwnerOrScopeIsAnotherRecord(t *testing.T) {
	f := newFixture(t)
	in := app.WriteInput{Scope: "common", Key: "notes", Title: "t", Body: ""}
	if _, err := f.svc.Write(agent("scribe"), in); err != nil {
		t.Fatal(err)
	}
	other := in
	other.Scope = "private"
	if _, err := f.svc.Write(agent("scribe"), other); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Write(agent("keeper"), in); err != nil {
		t.Fatal(err)
	}

	page, _ := f.svc.List(agent("founder"), app.ListInput{})
	if len(page.Items) != 3 {
		t.Fatalf("list %v", titles(page.Items))
	}
}

func TestWrite_Refusals(t *testing.T) {
	good := app.WriteInput{Scope: "common", Title: "t", Body: "b"}
	tests := []struct {
		name    string
		ctx     context.Context
		in      app.WriteInput
		wantErr error
	}{
		{"a human", caller(core.Caller{Kind: core.CallerHuman, Name: "sam", UID: -1}), good, core.ErrForbidden},
		{"a plugin", caller(core.Caller{Kind: core.CallerPlugin, Name: "github", UID: -1}), good, core.ErrForbidden},
		{"the host", caller(core.Caller{Kind: core.CallerHost, Name: "host", UID: -1}), good, core.ErrForbidden},
		{"nobody signed in", context.Background(), good, core.ErrUnauthorized},
		{"a caller without a name", caller(core.Caller{Kind: core.CallerHuman, UID: -1}), good, core.ErrUnauthorized},
		{"an agent the directory does not know", agent("ghost"), good, core.ErrUnauthorized},
		{"an unknown scope", agent("scribe"), app.WriteInput{Scope: "world", Title: "t"}, core.ErrInvalid},
		{"an empty title", agent("scribe"), app.WriteInput{Scope: "common", Title: ""}, core.ErrInvalid},
		{"a body over the limit", agent("scribe"), app.WriteInput{Scope: "common", Title: "t", Body: strings.Repeat("x", 65537)}, domain.ErrTooLarge},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)

			_, err := f.svc.Write(tt.ctx, tt.in)

			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("error = %v, want %v", err, tt.wantErr)
			}
			if page, _ := f.svc.List(agent("founder"), app.ListInput{}); len(page.Items) != 0 {
				t.Fatalf("a refused write saved %v", titles(page.Items))
			}
		})
	}
}

func TestWrite_ATooLargeBodyIsAlsoInvalid(t *testing.T) {
	f := newFixture(t)
	_, err := f.svc.Write(agent("scribe"), app.WriteInput{Scope: "common", Title: "t", Body: strings.Repeat("x", 65537)})
	if !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("error = %v", err)
	}
}

func TestWrite_ReportsAnUnavailableDirectory(t *testing.T) {
	f := newFixture(t)
	svc := app.New(f.repo, core.NewPorts(), f.fakes.Clock, f.fakes.IDs)

	_, err := svc.Write(agent("scribe"), app.WriteInput{Scope: "common", Title: "t"})

	if !errors.Is(err, core.ErrUnavailable) {
		t.Fatalf("error = %v", err)
	}
}
