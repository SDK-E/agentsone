// SPDX-License-Identifier: MIT

package app_test

import (
	"context"
	"errors"
	"testing"

	"github.com/SDK-E/agentsone/internal/auth/app"
	"github.com/SDK-E/agentsone/internal/auth/authtest"
	"github.com/SDK-E/agentsone/internal/core"
)

func bob() app.NewHuman { return app.NewHuman{Name: "bob", DisplayName: "Bob", Password: password} }

func TestCreateHuman_AddsALinuxUserAndAnAccount(t *testing.T) {
	t.Parallel()
	env := withHuman(t, "ada")
	h, err := env.Service.CreateHuman(context.Background(), authtest.Human("ada"), bob())
	if err != nil {
		t.Fatal(err)
	}
	if h.UID != 1001 || !env.Accounts.HasUser("bob") {
		t.Fatalf("human %+v", h)
	}
	hs, _ := env.Service.ListHumans(context.Background())
	if len(hs) != 2 {
		t.Fatalf("humans %+v", hs)
	}
}

func TestCreateHuman_IsAHumanPowerOnly(t *testing.T) {
	t.Parallel()
	env := withHuman(t, "ada")
	for name, by := range map[string]core.Caller{
		"root agent": authtest.Agent("founder"),
		"system":     {Kind: core.CallerSystem, Name: "x", IsRoot: true},
		"anonymous":  core.Anonymous,
		"plugin":     {Kind: core.CallerPlugin, Name: "notes"},
	} {
		if _, err := env.Service.CreateHuman(context.Background(), by, bob()); !errors.Is(err, core.ErrForbidden) {
			t.Errorf("%s: err %v", name, err)
		}
	}
	if env.Accounts.HasUser("bob") {
		t.Fatal("user created by a non-human")
	}
}

func TestCreateHuman_RejectsADuplicateName(t *testing.T) {
	t.Parallel()
	env := withHuman(t, "ada")
	n := bob()
	n.Name = "ada"
	if _, err := env.Service.CreateHuman(context.Background(), authtest.Human("ada"), n); !errors.Is(err, core.ErrConflict) {
		t.Fatalf("err %v", err)
	}
}

func TestCreateHuman_RejectsAnInvalidName(t *testing.T) {
	t.Parallel()
	env := withHuman(t, "ada")
	n := bob()
	n.Name = "Root"
	if _, err := env.Service.CreateHuman(context.Background(), authtest.Human("ada"), n); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("err %v", err)
	}
}

func TestCreateHuman_ReportsALinuxFailure(t *testing.T) {
	t.Parallel()
	env := withHuman(t, "ada")
	env.Accounts.Fail = errors.New("useradd exited 9")
	if _, err := env.Service.CreateHuman(context.Background(), authtest.Human("ada"), bob()); err == nil {
		t.Fatal("no error")
	}
	if _, err := env.Service.GetHuman(context.Background(), "bob"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("account stored without user: %v", err)
	}
}

func TestUpdateHuman_ChangesTheDisplayName(t *testing.T) {
	t.Parallel()
	env := withHuman(t, "ada")
	name := "Ada L."
	h, err := env.Service.UpdateHuman(context.Background(), authtest.Human("ada"), "ada", app.HumanChange{DisplayName: &name})
	if err != nil || h.DisplayName != name {
		t.Fatalf("human %+v err %v", h, err)
	}
	got, _ := env.Service.GetHuman(context.Background(), "ada")
	if got.DisplayName != name {
		t.Fatalf("stored %+v", got)
	}
}

func TestUpdateHuman_NewPasswordEndsOldSessions(t *testing.T) {
	t.Parallel()
	env := withHuman(t, "ada")
	ctx := context.Background()
	_, secret, _, _ := env.Service.SignIn(ctx, "ada", password, "")
	pw := "a brand new passphrase"
	if _, err := env.Service.UpdateHuman(ctx, authtest.Human("ada"), "ada", app.HumanChange{Password: &pw}); err != nil {
		t.Fatal(err)
	}
	if _, err := env.Service.SessionCaller(ctx, secret); !errors.Is(err, core.ErrUnauthorized) {
		t.Fatalf("old session works: %v", err)
	}
	if _, _, _, err := env.Service.SignIn(ctx, "ada", pw, ""); err != nil {
		t.Fatalf("new password: %v", err)
	}
}

func TestUpdateHuman_ForbidsAgents(t *testing.T) {
	t.Parallel()
	env := withHuman(t, "ada")
	name := "x"
	if _, err := env.Service.UpdateHuman(context.Background(), authtest.Agent("founder"), "ada", app.HumanChange{DisplayName: &name}); !errors.Is(err, core.ErrForbidden) {
		t.Fatalf("err %v", err)
	}
}

func TestUpdateHuman_UnknownIsNotFound(t *testing.T) {
	t.Parallel()
	env := withHuman(t, "ada")
	if _, err := env.Service.UpdateHuman(context.Background(), authtest.Human("ada"), "zed", app.HumanChange{}); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("err %v", err)
	}
}

func TestDeleteHuman_RemovesTheAccountAndItsSessions(t *testing.T) {
	t.Parallel()
	env := withHuman(t, "ada")
	ctx := context.Background()
	if _, err := env.Service.CreateHuman(ctx, authtest.Human("ada"), bob()); err != nil {
		t.Fatal(err)
	}
	_, secret, _, _ := env.Service.SignIn(ctx, "bob", password, "")
	if err := env.Service.DeleteHuman(ctx, authtest.Human("ada"), "bob"); err != nil {
		t.Fatal(err)
	}
	if env.Accounts.HasUser("bob") {
		t.Fatal("Linux user kept")
	}
	if _, err := env.Service.SessionCaller(ctx, secret); !errors.Is(err, core.ErrUnauthorized) {
		t.Fatalf("session kept: %v", err)
	}
}

func TestDeleteHuman_KeepsTheLastHuman(t *testing.T) {
	t.Parallel()
	env := withHuman(t, "ada")
	if err := env.Service.DeleteHuman(context.Background(), authtest.Human("ada"), "ada"); !errors.Is(err, core.ErrConflict) {
		t.Fatalf("err %v", err)
	}
}

func TestDeleteHuman_ForbidsAgents(t *testing.T) {
	t.Parallel()
	env := withHuman(t, "ada")
	if err := env.Service.DeleteHuman(context.Background(), authtest.Agent("founder"), "ada"); !errors.Is(err, core.ErrForbidden) {
		t.Fatalf("err %v", err)
	}
}

func TestDeleteHuman_UnknownIsNotFound(t *testing.T) {
	t.Parallel()
	env := withHuman(t, "ada")
	if err := env.Service.DeleteHuman(context.Background(), authtest.Human("ada"), "zed"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("err %v", err)
	}
}
