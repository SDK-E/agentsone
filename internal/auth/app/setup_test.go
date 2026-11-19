// SPDX-License-Identifier: MIT

package app_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/SDK-E/agentsone/internal/auth/app"
	"github.com/SDK-E/agentsone/internal/auth/authtest"
	"github.com/SDK-E/agentsone/internal/auth/domain"
	"github.com/SDK-E/agentsone/internal/core"
)

func phase(t *testing.T, env *authtest.Env) string {
	t.Helper()
	st, err := env.Service.SetupStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return st.Phase
}

func TestSetupStatus_WalksThroughFirstBoot(t *testing.T) {
	t.Parallel()
	env := authtest.New()
	ctx := context.Background()
	if p := phase(t, env); p != app.PhaseNeedsHuman {
		t.Fatalf("phase %q", p)
	}
	if _, err := env.Service.CreateFirstHuman(ctx, app.NewHuman{Name: "ada", Password: password}); err != nil {
		t.Fatal(err)
	}
	if p := phase(t, env); p != app.PhaseNeedsLicence {
		t.Fatalf("phase %q", p)
	}
	if _, err := env.Service.AcceptEngineLicence(ctx, authtest.Human("ada"), authtest.DefaultLicence.Version, true); err != nil {
		t.Fatal(err)
	}
	if p := phase(t, env); p != app.PhaseFetchingEngine {
		t.Fatalf("phase %q", p)
	}
	if err := env.Store.SaveEngineFetch(ctx, domain.EngineFetch{State: app.FetchReady}); err != nil {
		t.Fatal(err)
	}
	if p := phase(t, env); p != app.PhaseReady {
		t.Fatalf("phase %q", p)
	}
}

func TestCreateFirstHuman_RefusedOnceAHumanExists(t *testing.T) {
	t.Parallel()
	env := withHuman(t, "ada")
	if _, err := env.Service.CreateFirstHuman(context.Background(), bob()); !errors.Is(err, core.ErrConflict) {
		t.Fatalf("err %v", err)
	}
	if _, err := env.Service.GetHuman(context.Background(), "bob"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("second human created: %v", err)
	}
}

func TestEngineLicence_ShowsAcceptanceOfTheCurrentVersion(t *testing.T) {
	t.Parallel()
	env := withHuman(t, "ada")
	ctx := context.Background()
	v, err := env.Service.EngineLicence(ctx)
	if err != nil || v.Accepted != nil || v.Text == "" {
		t.Fatalf("view %+v err %v", v, err)
	}
	_, _ = env.Service.AcceptEngineLicence(ctx, authtest.Human("ada"), authtest.DefaultLicence.Version, true)
	v, _ = env.Service.EngineLicence(ctx)
	if v.Accepted == nil || v.Accepted.AcceptedBy != "ada" || !v.Accepted.AcceptedAt.Equal(env.Clock.Now()) {
		t.Fatalf("view %+v", v)
	}
}

func TestAcceptEngineLicence_IsAHumanPowerOnly(t *testing.T) {
	t.Parallel()
	env := withHuman(t, "ada")
	if _, err := env.Service.AcceptEngineLicence(context.Background(), authtest.Agent("founder"), authtest.DefaultLicence.Version, true); !errors.Is(err, core.ErrForbidden) {
		t.Fatalf("err %v", err)
	}
}

func TestAcceptEngineLicence_RejectsAnOldVersion(t *testing.T) {
	t.Parallel()
	env := withHuman(t, "ada")
	if _, err := env.Service.AcceptEngineLicence(context.Background(), authtest.Human("ada"), "2020-01", true); !errors.Is(err, core.ErrConflict) {
		t.Fatalf("err %v", err)
	}
}

func TestAcceptEngineLicence_RejectsDecline(t *testing.T) {
	t.Parallel()
	env := withHuman(t, "ada")
	if _, err := env.Service.AcceptEngineLicence(context.Background(), authtest.Human("ada"), authtest.DefaultLicence.Version, false); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("err %v", err)
	}
}

func TestAcceptEngineLicence_QueuesTheFetchAndEmitsAnEvent(t *testing.T) {
	t.Parallel()
	env := withHuman(t, "ada")
	f, err := env.Service.AcceptEngineLicence(context.Background(), authtest.Human("ada"), authtest.DefaultLicence.Version, true)
	if err != nil || f.State != app.FetchQueued {
		t.Fatalf("fetch %+v err %v", f, err)
	}
	found := false
	for _, ty := range eventTypes(env) {
		found = found || ty == "agentsone.setup.licence.accepted"
	}
	if !found {
		t.Fatalf("events %v", eventTypes(env))
	}
}

// eventData returns the data of the last event of type typ.
func eventData(t *testing.T, env *authtest.Env, typ string) string {
	t.Helper()
	for _, e := range slices.Backward(env.Events.Published()) {
		if e.Type == typ {
			return string(e.Data)
		}
	}
	t.Fatalf("no %s event in %v", typ, eventTypes(env))
	return ""
}

func TestCreateFirstHuman_EmitsFirstHumanCreatedWithTheNameOnly(t *testing.T) {
	t.Parallel()
	env := withHuman(t, "ada")
	if got := eventData(t, env, "agentsone.setup.first-human.created"); got != `{"human":"ada"}` {
		t.Fatalf("data %s", got)
	}
}

func TestAcceptEngineLicence_EventCarriesEngineVersionAndWho(t *testing.T) {
	t.Parallel()
	env := withHuman(t, "ada")
	if _, err := env.Service.AcceptEngineLicence(context.Background(), authtest.Human("ada"), authtest.DefaultLicence.Version, true); err != nil {
		t.Fatal(err)
	}
	want := `{"accepted_by":"ada","engine":"claude-code","version":"2026-11"}`
	if got := eventData(t, env, "agentsone.setup.licence.accepted"); got != want {
		t.Fatalf("data %s, want %s", got, want)
	}
}

func TestEngineFetch_WaitsForTheLicenceByDefault(t *testing.T) {
	t.Parallel()
	env := authtest.New()
	f, err := env.Service.EngineFetch(context.Background())
	if err != nil || f.State != app.FetchWaitingLicence {
		t.Fatalf("fetch %+v err %v", f, err)
	}
}
