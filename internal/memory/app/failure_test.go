// SPDX-License-Identifier: MIT

package app_test

import (
	"context"
	"errors"
	"testing"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/core/coretest"
	"github.com/SDK-E/agentsone/internal/memory/adapters/memory"
	"github.com/SDK-E/agentsone/internal/memory/app"
	"github.com/SDK-E/agentsone/internal/memory/domain"
)

var errDisk = errors.New("disk is full")

// brokenRepo is a repository whose storage fails, with real records behind it.
type brokenRepo struct{ *memory.Repository }

func (brokenRepo) Save(context.Context, domain.Record) error { return errDisk }
func (brokenRepo) FindByKey(context.Context, string, domain.Scope, string) (domain.Record, bool, error) {
	return domain.Record{}, false, errDisk
}
func (brokenRepo) List(context.Context, domain.ListQuery) (domain.Page, error) {
	return domain.Page{}, errDisk
}
func (brokenRepo) Search(context.Context, domain.SearchQuery) (domain.HitPage, error) {
	return domain.HitPage{}, errDisk
}
func (brokenRepo) Delete(context.Context, string) error { return errDisk }

func brokenService(t *testing.T) (*app.Service, *fixture) {
	t.Helper()
	f := newFixture(t)
	fakes, ports := coretest.NewFakes()
	fakes.Agents.Add(core.Agent{Name: "founder", IsRoot: true})
	return app.New(brokenRepo{f.repo}, ports, fakes.Clock, fakes.IDs), f
}

func TestStorageFailuresReachTheCaller(t *testing.T) {
	svc, f := brokenService(t)
	rec := f.write(t, "scribe", "common", "to delete")
	// The service has its own directory, which knows only founder.
	ctx := agent("founder")
	checks := map[string]error{}
	_, checks["write"] = svc.Write(ctx, app.WriteInput{Scope: "common", Title: "t"})
	_, checks["write with key"] = svc.Write(ctx, app.WriteInput{Scope: "common", Title: "t", Key: "k"})
	_, checks["list"] = svc.List(ctx, app.ListInput{})
	_, checks["search"] = svc.Search(ctx, app.SearchInput{Text: "x"})
	checks["delete"] = svc.Delete(ctx, rec.ID)

	for name, err := range checks {
		if !errors.Is(err, errDisk) {
			t.Errorf("%s: error = %v, want the storage error", name, err)
		}
	}
}

func TestSystemCallersReadEverything(t *testing.T) {
	f := newFixture(t)
	seed(f, t)

	page, err := f.svc.List(caller(core.Caller{Kind: core.CallerHost, Name: "host", UID: -1}), app.ListInput{})

	if err != nil || len(page.Items) != 5 {
		t.Fatalf("items %v, err %v", titles(page.Items), err)
	}
}

func TestACallerOfAnUnknownKindIsUnauthorized(t *testing.T) {
	f := newFixture(t)
	_, err := f.svc.List(caller(core.Caller{Kind: "robot", Name: "r2", UID: -1}), app.ListInput{})
	if !errors.Is(err, core.ErrUnauthorized) {
		t.Fatalf("error = %v", err)
	}
}
