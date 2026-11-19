// SPDX-License-Identifier: MIT

package app_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/auth/app"
	"github.com/SDK-E/agentsone/internal/auth/authtest"
	"github.com/SDK-E/agentsone/internal/auth/domain"
	"github.com/SDK-E/agentsone/internal/core"
)

const password = "correct horse battery"

func withHuman(t *testing.T, name string) *authtest.Env {
	t.Helper()
	env := authtest.New()
	if _, err := env.Service.CreateFirstHuman(context.Background(), app.NewHuman{Name: name, DisplayName: "Ada", Password: password}); err != nil {
		t.Fatal(err)
	}
	return env
}

func eventTypes(env *authtest.Env) []string {
	var out []string
	for _, e := range env.Events.Published() {
		out = append(out, e.Type)
	}
	return out
}

func TestSignIn_StartsASessionTheCookieResolves(t *testing.T) {
	t.Parallel()
	env := withHuman(t, "ada")
	ctx := context.Background()
	_, secret, h, err := env.Service.SignIn(ctx, "ada", password, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if !h.LastLoginAt.Equal(env.Clock.Now()) {
		t.Fatalf("last login %v", h.LastLoginAt)
	}
	c, err := env.Service.SessionCaller(ctx, secret)
	if err != nil || c.Kind != core.CallerHuman || c.Name != "ada" || c.IsRoot {
		t.Fatalf("caller %+v err %v", c, err)
	}
	if !slices.Contains(eventTypes(env), "agentsone.auth.login.succeeded") {
		t.Fatalf("events %v", eventTypes(env))
	}
}

func TestSignIn_RejectsAWrongPassword(t *testing.T) {
	t.Parallel()
	env := withHuman(t, "ada")
	_, _, _, err := env.Service.SignIn(context.Background(), "ada", "nope", "127.0.0.1")
	if !errors.Is(err, core.ErrUnauthorized) {
		t.Fatalf("err %v", err)
	}
	if !slices.Contains(eventTypes(env), "agentsone.auth.login.failed") {
		t.Fatalf("events %v", eventTypes(env))
	}
}

func TestSignIn_RejectsALinuxUserWhoIsNotAHuman(t *testing.T) {
	t.Parallel()
	env := withHuman(t, "ada")
	if _, err := env.Accounts.CreateUser(context.Background(), "svc", password); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := env.Service.SignIn(context.Background(), "svc", password, ""); !errors.Is(err, core.ErrUnauthorized) {
		t.Fatalf("err %v", err)
	}
}

func TestSignIn_ThrottlesRepeatedFailures(t *testing.T) {
	t.Parallel()
	env := withHuman(t, "ada")
	ctx := context.Background()
	for range domain.MaxFailures {
		_, _, _, _ = env.Service.SignIn(ctx, "ada", "nope", "")
	}
	_, _, _, err := env.Service.SignIn(ctx, "ada", password, "")
	var tooMany *app.TooManyAttempts
	if !errors.As(err, &tooMany) || tooMany.RetryAfter != domain.FailureWindow || !errors.Is(err, app.ErrTooManyAttempts) {
		t.Fatalf("err %v", err)
	}
	env.Clock.Advance(domain.FailureWindow)
	if _, _, _, err := env.Service.SignIn(ctx, "ada", password, ""); err != nil {
		t.Fatalf("still throttled: %v", err)
	}
}

func TestSessionCaller_RejectsAnExpiredSession(t *testing.T) {
	t.Parallel()
	env := withHuman(t, "ada")
	ctx := context.Background()
	_, secret, _, _ := env.Service.SignIn(ctx, "ada", password, "")
	env.Clock.Advance(domain.SessionTTL)
	if _, err := env.Service.SessionCaller(ctx, secret); !errors.Is(err, core.ErrUnauthorized) {
		t.Fatalf("err %v", err)
	}
}

func TestSessionCaller_RejectsAnUnknownSecret(t *testing.T) {
	t.Parallel()
	env := withHuman(t, "ada")
	if _, err := env.Service.SessionCaller(context.Background(), "made-up"); !errors.Is(err, core.ErrUnauthorized) {
		t.Fatalf("err %v", err)
	}
}

func TestSignOut_EndsTheSession(t *testing.T) {
	t.Parallel()
	env := withHuman(t, "ada")
	ctx := context.Background()
	_, secret, _, _ := env.Service.SignIn(ctx, "ada", password, "")
	if err := env.Service.SignOut(ctx, secret); err != nil {
		t.Fatal(err)
	}
	if _, err := env.Service.SessionCaller(ctx, secret); !errors.Is(err, core.ErrUnauthorized) {
		t.Fatalf("session survived sign-out: %v", err)
	}
	if err := env.Service.SignOut(ctx, secret); !errors.Is(err, core.ErrUnauthorized) {
		t.Fatalf("second sign-out: %v", err)
	}
	if !slices.Contains(eventTypes(env), "agentsone.auth.logout") {
		t.Fatalf("events %v", eventTypes(env))
	}
}

func TestIssue_TokenVerifiesToItsSubjectWithScopes(t *testing.T) {
	t.Parallel()
	env := authtest.New()
	ctx := context.Background()
	tok, err := env.Service.Issue(ctx, authtest.Agent("founder"), []string{"mcp:write"}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	c, err := env.Service.Verify(ctx, tok.Value)
	if err != nil {
		t.Fatal(err)
	}
	if c.Kind != core.CallerAgent || c.Name != "founder" || c.IsRoot || !c.Has("mcp:write") || c.Has("bus:write") {
		t.Fatalf("caller %+v", c)
	}
}

func TestIssue_RejectsABadScope(t *testing.T) {
	t.Parallel()
	env := authtest.New()
	if _, err := env.Service.Issue(context.Background(), authtest.Agent("a"), []string{"all"}, time.Hour); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("err %v", err)
	}
}

func TestVerify_RejectsUnknownExpiredAndMalformedTokens(t *testing.T) {
	t.Parallel()
	env := authtest.New()
	ctx := context.Background()
	tok, _ := env.Service.Issue(ctx, authtest.Agent("a"), nil, time.Minute)
	for name, value := range map[string]string{"malformed": "garbage", "unknown": "aot_ffff_secret"} {
		if _, err := env.Service.Verify(ctx, value); !errors.Is(err, core.ErrUnauthorized) {
			t.Errorf("%s: err %v", name, err)
		}
	}
	env.Clock.Advance(time.Minute)
	if _, err := env.Service.Verify(ctx, tok.Value); !errors.Is(err, core.ErrUnauthorized) {
		t.Fatalf("expired: err %v", err)
	}
}

func tokenID(t *testing.T, value string) string {
	t.Helper()
	id, _, err := domain.ParseTokenValue(value)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestRevokeToken_StopsTheToken(t *testing.T) {
	t.Parallel()
	env := authtest.New()
	ctx := context.Background()
	tok, _ := env.Service.Issue(ctx, authtest.Agent("scout"), nil, time.Hour)
	if err := env.Service.RevokeToken(ctx, authtest.Agent("scout"), tokenID(t, tok.Value)); err != nil {
		t.Fatal(err)
	}
	if _, err := env.Service.Verify(ctx, tok.Value); !errors.Is(err, core.ErrUnauthorized) {
		t.Fatalf("revoked token works: %v", err)
	}
	if got := env.Audit.Entries(); len(got) == 0 || got[len(got)-1].Action != "token.revoke" {
		t.Fatalf("audit %+v", got)
	}
}

func TestRevokeToken_ForbidsAnotherAgent(t *testing.T) {
	t.Parallel()
	env := authtest.New()
	ctx := context.Background()
	tok, _ := env.Service.Issue(ctx, authtest.Agent("scout"), nil, time.Hour)
	if err := env.Service.RevokeToken(ctx, authtest.Agent("cto"), tokenID(t, tok.Value)); !errors.Is(err, core.ErrForbidden) {
		t.Fatalf("err %v", err)
	}
}

func TestRevokeToken_UnknownIDIsNotFound(t *testing.T) {
	t.Parallel()
	env := authtest.New()
	if err := env.Service.RevokeToken(context.Background(), authtest.Human("ada"), "ffff"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("err %v", err)
	}
}

func TestHumanByUID_FindsOnlyHumans(t *testing.T) {
	t.Parallel()
	env := withHuman(t, "ada")
	ctx := context.Background()
	if name, ok := env.Service.HumanByUID(ctx, 1000); !ok || name != "ada" {
		t.Fatalf("got %q %v", name, ok)
	}
	if _, ok := env.Service.HumanByUID(ctx, 4242); ok {
		t.Fatal("unknown uid found")
	}
}
