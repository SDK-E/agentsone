// SPDX-License-Identifier: MIT

package app_test

import (
	"context"
	"errors"
	"testing"

	"github.com/SDK-E/agentsone/internal/auth/app"
	"github.com/SDK-E/agentsone/internal/auth/authtest"
	"github.com/SDK-E/agentsone/internal/auth/domain"
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
	if h.UID != 1001 {
		t.Fatalf("human %+v", h)
	}
	if _, _, _, err := env.Service.SignIn(context.Background(), "bob", password, ""); err != nil {
		t.Fatalf("new human cannot sign in: %v", err)
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
	if _, err := env.Service.GetHuman(context.Background(), "bob"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("account created by a non-human: %v", err)
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

func TestCreateHuman_RejectsAShortPassword(t *testing.T) {
	t.Parallel()
	env := withHuman(t, "ada")
	n := bob()
	n.Password = "short"
	if _, err := env.Service.CreateHuman(context.Background(), authtest.Human("ada"), n); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("err %v", err)
	}
}

func ptr(s string) *string { return &s }

const newPassword = "a brand new passphrase"

func TestUpdateHuman_RejectsAShortPassword(t *testing.T) {
	t.Parallel()
	env := withHuman(t, "ada")
	c := app.HumanChange{Password: ptr("short"), CurrentPassword: ptr(password)}
	if _, err := env.Service.UpdateHuman(context.Background(), authtest.Human("ada"), "ada", c); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("err %v", err)
	}
}

func TestUpdateHuman_ChangesTheDisplayName(t *testing.T) {
	t.Parallel()
	env := withHuman(t, "ada")
	name := "Ada L."
	u, err := env.Service.UpdateHuman(context.Background(), authtest.Human("ada"), "ada", app.HumanChange{DisplayName: &name})
	if err != nil || u.Human.DisplayName != name || u.Session != "" {
		t.Fatalf("update %+v err %v", u, err)
	}
	got, _ := env.Service.GetHuman(context.Background(), "ada")
	if got.DisplayName != name {
		t.Fatalf("stored %+v", got)
	}
}

func TestUpdateHuman_OwnPasswordEndsOtherSessionsAndRotatesTheCallers(t *testing.T) {
	t.Parallel()
	env := withHuman(t, "ada")
	ctx := context.Background()
	_, mine, _, _ := env.Service.SignIn(ctx, "ada", password, "")
	_, other, _, _ := env.Service.SignIn(ctx, "ada", password, "")
	u, err := env.Service.UpdateHuman(ctx, authtest.Human("ada"), "ada", app.HumanChange{Password: ptr(newPassword), CurrentPassword: ptr(password)})
	if err != nil {
		t.Fatal(err)
	}
	for name, secret := range map[string]string{"caller's old": mine, "other": other} {
		if _, err := env.Service.SessionCaller(ctx, secret); !errors.Is(err, core.ErrUnauthorized) {
			t.Fatalf("%s session still works: %v", name, err)
		}
	}
	c, err := env.Service.SessionCaller(ctx, u.Session)
	if err != nil || c.Name != "ada" {
		t.Fatalf("rotated session: %+v %v", c, err)
	}
	if _, _, _, err := env.Service.SignIn(ctx, "ada", newPassword, ""); err != nil {
		t.Fatalf("new password: %v", err)
	}
}

func TestUpdateHuman_OwnPasswordNeedsTheCurrentOne(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		current *string
		want    error
	}{
		"missing": {nil, app.ErrCurrentPasswordMissing},
		"wrong":   {ptr("not the password at all"), app.ErrCurrentPasswordWrong},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			env := withHuman(t, "ada")
			ctx := context.Background()
			_, secret, _, _ := env.Service.SignIn(ctx, "ada", password, "")
			_, err := env.Service.UpdateHuman(ctx, authtest.Human("ada"), "ada", app.HumanChange{Password: ptr(newPassword), CurrentPassword: c.current})
			if !errors.Is(err, c.want) || !errors.Is(err, core.ErrInvalid) {
				t.Fatalf("err %v", err)
			}
			if _, err := env.Service.SessionCaller(ctx, secret); err != nil {
				t.Fatalf("refused change ended the session: %v", err)
			}
			if _, _, _, err := env.Service.SignIn(ctx, "ada", password, ""); err != nil {
				t.Fatalf("old password changed: %v", err)
			}
		})
	}
}

func TestUpdateHuman_WrongCurrentPasswordIsThrottled(t *testing.T) {
	t.Parallel()
	env := withHuman(t, "ada")
	ctx := context.Background()
	c := app.HumanChange{Password: ptr(newPassword), CurrentPassword: ptr("not the password at all")}
	for range domain.MaxFailures {
		_, _ = env.Service.UpdateHuman(ctx, authtest.Human("ada"), "ada", c)
	}
	c.CurrentPassword = ptr(password)
	if _, err := env.Service.UpdateHuman(ctx, authtest.Human("ada"), "ada", c); !errors.Is(err, app.ErrCurrentPasswordThrottled) {
		t.Fatalf("err %v", err)
	}
	env.Clock.Advance(domain.FailureWindow)
	if _, err := env.Service.UpdateHuman(ctx, authtest.Human("ada"), "ada", c); err != nil {
		t.Fatalf("after the window: %v", err)
	}
}

func TestUpdateHuman_ResetOfAnotherHumanEndsAllTheirSessions(t *testing.T) {
	t.Parallel()
	env := withHuman(t, "ada")
	ctx := context.Background()
	if _, err := env.Service.CreateHuman(ctx, authtest.Human("ada"), bob()); err != nil {
		t.Fatal(err)
	}
	_, bobs, _, _ := env.Service.SignIn(ctx, "bob", password, "")
	_, adas, _, _ := env.Service.SignIn(ctx, "ada", password, "")
	u, err := env.Service.UpdateHuman(ctx, authtest.Human("ada"), "bob", app.HumanChange{Password: ptr(newPassword)})
	if err != nil || u.Session != "" {
		t.Fatalf("update %+v err %v", u, err)
	}
	if _, err := env.Service.SessionCaller(ctx, bobs); !errors.Is(err, core.ErrUnauthorized) {
		t.Fatalf("bob's session still works: %v", err)
	}
	if _, err := env.Service.SessionCaller(ctx, adas); err != nil {
		t.Fatalf("ada's session ended: %v", err)
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
	if _, err := env.Service.GetHuman(ctx, "bob"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("account kept: %v", err)
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
