// SPDX-License-Identifier: MIT

package app_test

import (
	"errors"
	"testing"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/events/domain"
)

func processReg(name string) domain.Registration {
	return domain.Registration{
		Name: name, Kind: domain.KindProcess, Description: "Runs a command.",
		EventTypes: []string{"agentsone." + name + ".*"}, Command: []string{"/usr/bin/true"},
	}
}

func hookReg(name string) domain.Registration {
	return domain.Registration{Name: name, Kind: domain.KindWebhook, Description: "A hook.", EventTypes: []string{"agentsone." + name + ".*"}}
}

func TestSeedBuiltins_StoresTheNineOnceOnly(t *testing.T) {
	f := newFixture(t)

	if err := f.svc.SeedBuiltins(t.Context()); err != nil {
		t.Fatal(err)
	}

	list, err := f.svc.ListEmitters(t.Context(), "", 50)
	if err != nil || len(list) != 9 {
		t.Fatalf("emitters = %d, %v, want 9", len(list), err)
	}
	for _, e := range list {
		if e.Kind != domain.KindBuiltin || !e.Enabled || e.ID == "" || e.CreatedAt.IsZero() {
			t.Fatalf("seeded emitter = %+v", e)
		}
	}
}

func TestRegisterEmitter_WebhookGetsAPathAndASecretShownOnce(t *testing.T) {
	f := newFixture(t)

	got, err := f.svc.RegisterEmitter(asAgent("builder"), hookReg("github-push"))

	if err != nil {
		t.Fatal(err)
	}
	e := got.Emitter
	if e.WebhookPath != "/v1/webhooks/"+e.ID || e.Owner != "agent:builder" || got.Secret == "" || !e.Enabled {
		t.Fatalf("registered = %+v, secret %q", e, got.Secret)
	}
	if want := f.auditActions(); want[len(want)-1] != "emitter.register" {
		t.Fatalf("audit actions = %v", want)
	}
	fetched, err := f.svc.GetEmitter(t.Context(), e.ID)
	if err != nil || fetched.Name != "github-push" {
		t.Fatalf("GetEmitter() = %+v, %v", fetched, err)
	}
}

func TestRegisterEmitter_ProcessEmittersNeedAPrivilegedCaller(t *testing.T) {
	f := newFixture(t)

	if _, err := f.svc.RegisterEmitter(asAgent("builder"), processReg("watcher")); !errors.Is(err, core.ErrForbidden) {
		t.Fatalf("agent register error = %v, want ErrForbidden", err)
	}
	got, err := f.svc.RegisterEmitter(asRootAgent("founder"), processReg("watcher"))
	if err != nil {
		t.Fatal(err)
	}
	if got.Emitter.Unit != "emitter@"+got.Emitter.ID+".service" || len(f.units.started) != 1 || f.units.started[0] != got.Emitter.Unit {
		t.Fatalf("unit = %q, started %v", got.Emitter.Unit, f.units.started)
	}
	if got.Secret != "" {
		t.Fatal("a process emitter got a webhook secret")
	}
}

func TestRegisterEmitter_Refusals(t *testing.T) {
	f := newFixture(t)
	if _, err := f.svc.RegisterEmitter(asAgent("builder"), hookReg("hook")); err != nil {
		t.Fatal(err)
	}
	bad := hookReg("Bad Name")

	if _, err := f.svc.RegisterEmitter(asAgent("builder"), hookReg("hook")); !errors.Is(err, core.ErrConflict) {
		t.Fatalf("duplicate name error = %v, want ErrConflict", err)
	}
	if _, err := f.svc.RegisterEmitter(asAgent("builder"), bad); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("bad name error = %v, want ErrInvalid", err)
	}
	if _, err := f.svc.RegisterEmitter(core.WithCaller(t.Context(), core.Anonymous), hookReg("other")); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("anonymous caller error = %v, want ErrInvalid", err)
	}
}

func TestRegisterEmitter_AFailingUnitLeavesItDisabled(t *testing.T) {
	f := newFixture(t)
	f.units.err = errors.New("no systemd")

	_, err := f.svc.RegisterEmitter(asRootAgent("founder"), processReg("watcher"))

	if !errors.Is(err, core.ErrUnavailable) {
		t.Fatalf("error = %v, want ErrUnavailable", err)
	}
	e, err := f.emitters.ByName(t.Context(), "watcher")
	if err != nil || e.Enabled {
		t.Fatalf("emitter after failed start = %+v, %v", e, err)
	}
}

func TestEnableAndDisableEmitter(t *testing.T) {
	f := newFixture(t)
	got, err := f.svc.RegisterEmitter(asRootAgent("founder"), processReg("watcher"))
	if err != nil {
		t.Fatal(err)
	}
	id := got.Emitter.ID
	if _, err := f.svc.DisableEmitter(asAgent("builder"), id); !errors.Is(err, core.ErrForbidden) {
		t.Fatalf("stranger disable error = %v, want ErrForbidden", err)
	}

	off, err := f.svc.DisableEmitter(asRootAgent("founder"), id)
	if err != nil || off.Enabled || len(f.units.stopped) != 1 {
		t.Fatalf("DisableEmitter() = %+v, %v, stopped %v", off, err, f.units.stopped)
	}
	again, err := f.svc.DisableEmitter(asRootAgent("founder"), id)
	if err != nil || again.Enabled || len(f.units.stopped) != 1 {
		t.Fatalf("second DisableEmitter() stopped the unit again: %v, %v", f.units.stopped, err)
	}
	on, err := f.svc.EnableEmitter(asRootAgent("founder"), id)
	if err != nil || !on.Enabled || len(f.units.started) != 2 {
		t.Fatalf("EnableEmitter() = %+v, %v, started %v", on, err, f.units.started)
	}
	if _, err := f.svc.EnableEmitter(asRootAgent("founder"), "emt_missing"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("unknown id error = %v, want ErrNotFound", err)
	}
	f.units.err = errors.New("no systemd")
	if _, err := f.svc.DisableEmitter(asRootAgent("founder"), id); !errors.Is(err, core.ErrUnavailable) {
		t.Fatalf("failing unit error = %v, want ErrUnavailable", err)
	}
}

func TestEnableEmitter_OwnerMayChangeItsWebhook(t *testing.T) {
	f := newFixture(t)
	got, err := f.svc.RegisterEmitter(asAgent("builder"), hookReg("hook"))
	if err != nil {
		t.Fatal(err)
	}

	off, err := f.svc.DisableEmitter(asAgent("builder"), got.Emitter.ID)

	if err != nil || off.Enabled {
		t.Fatalf("owner disable = %+v, %v", off, err)
	}
}
