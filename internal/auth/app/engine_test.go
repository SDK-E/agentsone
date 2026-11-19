// SPDX-License-Identifier: MIT

package app_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/auth/app"
	"github.com/SDK-E/agentsone/internal/auth/authtest"
	"github.com/SDK-E/agentsone/internal/auth/domain"
	"github.com/SDK-E/agentsone/internal/core"
)

var _ core.EngineSetup = (*app.Service)(nil)

func TestLicenceAccepted_OnlyForTheCurrentVersion(t *testing.T) {
	t.Parallel()
	env := withHuman(t, "ada")
	ctx := context.Background()
	if ok, err := env.Service.LicenceAccepted(ctx); err != nil || ok {
		t.Fatalf("before accept: %v %v", ok, err)
	}
	if err := env.Store.SaveLicence(ctx, domain.Licence{Version: "2020-01", AcceptedBy: "ada", AcceptedAt: env.Clock.Now()}); err != nil {
		t.Fatal(err)
	}
	if ok, err := env.Service.LicenceAccepted(ctx); err != nil || ok {
		t.Fatalf("old version counted: %v %v", ok, err)
	}
	if _, err := env.Service.AcceptEngineLicence(ctx, authtest.Human("ada"), authtest.DefaultLicence.Version, true); err != nil {
		t.Fatal(err)
	}
	if ok, err := env.Service.LicenceAccepted(ctx); err != nil || !ok {
		t.Fatalf("after accept: %v %v", ok, err)
	}
}

func TestSaveFetchStatus_ShowsInSetupStatus(t *testing.T) {
	t.Parallel()
	env := withHuman(t, "ada")
	ctx := context.Background()
	at := env.Clock.Now().Add(time.Minute)
	err := env.Service.SaveFetchStatus(ctx, core.EngineFetchStatus{
		State: app.FetchDownloading, BytesDone: 25, BytesTotal: 100, Version: "2.1.0", UpdatedAt: at,
	})
	if err != nil {
		t.Fatal(err)
	}
	f, err := env.Service.EngineFetch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := domain.EngineFetch{State: app.FetchDownloading, Progress: 0.25, BytesDone: 25, BytesTotal: 100, Version: "2.1.0", UpdatedAt: at}
	if f != want {
		t.Fatalf("fetch %+v, want %+v", f, want)
	}
}

func TestSaveFetchStatus_ProgressRules(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   core.EngineFetchStatus
		want float64
	}{
		{"unknown total", core.EngineFetchStatus{State: app.FetchDownloading, BytesDone: 10}, 0},
		{"done past total", core.EngineFetchStatus{State: app.FetchDownloading, BytesDone: 12, BytesTotal: 10}, 1},
		{"ready is complete", core.EngineFetchStatus{State: app.FetchReady}, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			env := authtest.New()
			ctx := context.Background()
			if err := env.Service.SaveFetchStatus(ctx, c.in); err != nil {
				t.Fatal(err)
			}
			f, _ := env.Service.EngineFetch(ctx)
			if f.Progress != c.want {
				t.Fatalf("progress %v, want %v", f.Progress, c.want)
			}
		})
	}
}

func TestSaveFetchStatus_StampsTheTimeAndKeepsErrorsForFailures(t *testing.T) {
	t.Parallel()
	env := authtest.New()
	ctx := context.Background()
	if err := env.Service.SaveFetchStatus(ctx, core.EngineFetchStatus{State: app.FetchVerifying, Error: "stale"}); err != nil {
		t.Fatal(err)
	}
	f, _ := env.Service.EngineFetch(ctx)
	if f.Error != "" || !f.UpdatedAt.Equal(env.Clock.Now()) {
		t.Fatalf("fetch %+v", f)
	}
	if err := env.Service.SaveFetchStatus(ctx, core.EngineFetchStatus{State: app.FetchFailed, Error: "The download stopped."}); err != nil {
		t.Fatal(err)
	}
	if f, _ = env.Service.EngineFetch(ctx); f.Error != "The download stopped." {
		t.Fatalf("fetch %+v", f)
	}
}

func TestSaveFetchStatus_RefusesBadInput(t *testing.T) {
	t.Parallel()
	for name, in := range map[string]core.EngineFetchStatus{
		"unknown state":  {State: "exploded"},
		"empty state":    {},
		"negative bytes": {State: app.FetchDownloading, BytesDone: -1},
		"negative total": {State: app.FetchDownloading, BytesTotal: -1},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			env := authtest.New()
			if err := env.Service.SaveFetchStatus(context.Background(), in); !errors.Is(err, core.ErrInvalid) {
				t.Fatalf("err %v", err)
			}
		})
	}
}
