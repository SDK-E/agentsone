// SPDX-License-Identifier: MIT

package app_test

import (
	"errors"
	"testing"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/events/app"
	"github.com/SDK-E/agentsone/internal/events/domain"
)

func TestNoUnits_IsNotAvailableStartFailsAndStopIsHarmless(t *testing.T) {
	var units app.NoUnits

	if units.Available() {
		t.Fatal("NoUnits reports itself available")
	}
	if err := units.Start(t.Context(), domain.Emitter{}); !errors.Is(err, core.ErrUnavailable) {
		t.Fatalf("Start() error = %v, want ErrUnavailable", err)
	}
	if err := units.Stop(t.Context(), domain.Emitter{}); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
}

func TestRegisterEmitter_WithoutAUnitControllerAProcessEmitterIsRefusedAndNotStored(t *testing.T) {
	f := newFixtureWith(t, func(d *app.Deps) { d.Units = app.NoUnits{} })

	_, err := f.svc.RegisterEmitter(asRootAgent("founder"), processReg("watcher"))

	if !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("RegisterEmitter() error = %v, want ErrInvalid", err)
	}
	if _, err := f.emitters.ByName(t.Context(), "watcher"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("a refused process emitter was stored: %v", err)
	}
}
