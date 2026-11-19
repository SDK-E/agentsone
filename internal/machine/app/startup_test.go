// SPDX-License-Identifier: MIT

package app_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/machine/app"
	"github.com/SDK-E/agentsone/internal/machine/domain"
)

// slowApt blocks Installed until released.
type slowApt struct {
	*fakeApt
	release chan struct{}
}

func (a slowApt) Installed(c context.Context) ([]domain.Package, error) {
	<-a.release
	return a.fakeApt.Installed(c)
}

func TestBeginReapplyReturnsAtOnceAndOpensTheGateWhenDone(t *testing.T) {
	release := make(chan struct{})
	r := newRig(t, func(d *app.Deps) { d.Packages = slowApt{fakeApt: newFakeApt("bash"), release: release} })
	gate := app.NewStartupGate()
	done := make(chan domain.Report, 1)
	r.m.BeginReapply(ctx(), time.Minute, func(rep domain.Report) { gate.Open(); done <- rep })
	if s := r.m.RecipeReport().Service(); s.ActiveState != domain.ServiceActivating {
		t.Fatalf("while applying: %+v", s)
	}
	waiting, cancel := context.WithCancel(ctx())
	cancel()
	if err := gate.Wait(waiting); !errors.Is(err, context.Canceled) {
		t.Fatalf("gate open before the recipe finished: %v", err)
	}
	close(release)
	if rep := <-done; rep.State != domain.RecipeApplied {
		t.Fatalf("report %+v", rep)
	}
	if err := gate.Wait(ctx()); err != nil {
		t.Fatalf("gate after the recipe: %v", err)
	}
	gate.Open() // opening twice is harmless
}

func TestBeginReapplyIsBounded(t *testing.T) {
	r := newRig(t, func(d *app.Deps) {
		d.Packages = slowApt{fakeApt: newFakeApt(), release: make(chan struct{})}
	})
	_ = r.recipes.Save(ctx(), domain.Recipe{Packages: []string{"jq"}})
	done := make(chan domain.Report, 1)
	r.m.BeginReapply(ctx(), 0, func(rep domain.Report) { done <- rep })
	if rep := <-done; rep.State != domain.RecipeFailed {
		t.Fatalf("report %+v", rep)
	}
}
