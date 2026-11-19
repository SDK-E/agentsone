// SPDX-License-Identifier: MIT

package authtest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/SDK-E/agentsone/internal/auth/domain"
	"github.com/SDK-E/agentsone/internal/core"
)

// Repo is every identity repository in one value.
type Repo interface {
	domain.Sessions
	domain.Tokens
	domain.Humans
	domain.Setup
}

var t0 = time.Date(2026, 11, 19, 20, 0, 0, 123456789, time.UTC)

// RunRepositoryContract runs the identity repository contract against fresh
// repositories made by newRepo.
func RunRepositoryContract(t *testing.T, newRepo func(t *testing.T) Repo) {
	t.Helper()
	t.Run("humans round trip", func(t *testing.T) { humansRoundTrip(t, newRepo(t)) })
	t.Run("duplicate human conflicts", func(t *testing.T) { duplicateHuman(t, newRepo(t)) })
	t.Run("unknown human is not found", func(t *testing.T) { unknownHuman(t, newRepo(t)) })
	t.Run("sessions round trip and end", func(t *testing.T) { sessions(t, newRepo(t)) })
	t.Run("tokens round trip and revoke", func(t *testing.T) { tokens(t, newRepo(t)) })
	t.Run("setup state starts empty and is kept", func(t *testing.T) { setup(t, newRepo(t)) })
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func ada() domain.Human {
	return domain.Human{Name: "ada", DisplayName: "Ada", UID: 1000, CreatedAt: t0}
}

func humansRoundTrip(t *testing.T, r Repo) {
	ctx := context.Background()
	must(t, r.AddHuman(ctx, ada()))
	must(t, r.AddHuman(ctx, domain.Human{Name: "bob", DisplayName: "Bob", UID: 1001, CreatedAt: t0}))
	got, err := r.Human(ctx, "ada")
	must(t, err)
	if diff := cmp.Diff(ada(), got); diff != "" {
		t.Fatalf("human (-want +got):\n%s", diff)
	}
	byUID, err := r.HumanByUID(ctx, 1001)
	must(t, err)
	if byUID.Name != "bob" {
		t.Fatalf("by uid %+v", byUID)
	}
	h := ada()
	h.DisplayName, h.LastLoginAt = "Ada L.", t0.Add(time.Hour)
	must(t, r.UpdateHuman(ctx, h))
	all, err := r.ListHumans(ctx)
	must(t, err)
	if len(all) != 2 || all[0].Name != "ada" || all[0].DisplayName != "Ada L." || !all[0].LastLoginAt.Equal(h.LastLoginAt) {
		t.Fatalf("list %+v", all)
	}
	must(t, r.DeleteHuman(ctx, "bob"))
	if _, err := r.Human(ctx, "bob"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("deleted human: %v", err)
	}
}

func duplicateHuman(t *testing.T, r Repo) {
	ctx := context.Background()
	must(t, r.AddHuman(ctx, ada()))
	if err := r.AddHuman(ctx, ada()); !errors.Is(err, core.ErrConflict) {
		t.Fatalf("err %v", err)
	}
}

func unknownHuman(t *testing.T, r Repo) {
	ctx := context.Background()
	if _, err := r.Human(ctx, "zed"); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("Human: %v", err)
	}
	if _, err := r.HumanByUID(ctx, 9); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("HumanByUID: %v", err)
	}
	if err := r.UpdateHuman(ctx, ada()); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("UpdateHuman: %v", err)
	}
	if err := r.DeleteHuman(ctx, "zed"); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("DeleteHuman: %v", err)
	}
	all, err := r.ListHumans(ctx)
	if err != nil || len(all) != 0 {
		t.Errorf("ListHumans: %v %v", all, err)
	}
}

func sessions(t *testing.T, r Repo) {
	ctx := context.Background()
	must(t, r.AddHuman(ctx, ada()))
	s := domain.Session{ID: "abc", Human: "ada", CreatedAt: t0, ExpiresAt: t0.Add(time.Hour)}
	must(t, r.SaveSession(ctx, s))
	must(t, r.SaveSession(ctx, domain.Session{ID: "def", Human: "ada", CreatedAt: t0, ExpiresAt: t0.Add(time.Hour)}))
	got, err := r.Session(ctx, "abc")
	must(t, err)
	if diff := cmp.Diff(s, got); diff != "" {
		t.Fatalf("session (-want +got):\n%s", diff)
	}
	must(t, r.DeleteSession(ctx, "abc"))
	must(t, r.DeleteSession(ctx, "abc"))
	if _, err := r.Session(ctx, "abc"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("deleted session: %v", err)
	}
	must(t, r.DeleteHumanSessions(ctx, "ada"))
	if _, err := r.Session(ctx, "def"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("human sessions kept: %v", err)
	}
}

func tokens(t *testing.T, r Repo) {
	ctx := context.Background()
	tok := domain.Token{
		ID: "t1", Subject: core.Caller{Kind: core.CallerPlugin, Name: "notes", UID: -1},
		Scopes: []string{"memory:read"}, IssuedAt: t0, ExpiresAt: t0.Add(time.Hour),
	}
	tok.Hash[0] = 7
	must(t, r.SaveToken(ctx, tok))
	got, err := r.Token(ctx, "t1")
	must(t, err)
	if diff := cmp.Diff(tok, got); diff != "" {
		t.Fatalf("token (-want +got):\n%s", diff)
	}
	must(t, r.RevokeToken(ctx, "t1", t0.Add(time.Minute)))
	must(t, r.RevokeToken(ctx, "t1", t0.Add(2*time.Minute)))
	got, _ = r.Token(ctx, "t1")
	if !got.RevokedAt.Equal(t0.Add(time.Minute)) {
		t.Fatalf("revoked at %v, want the first revocation", got.RevokedAt)
	}
	if _, err := r.Token(ctx, "nope"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("unknown token: %v", err)
	}
	if err := r.RevokeToken(ctx, "nope", t0); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("revoke unknown: %v", err)
	}
}

func setup(t *testing.T, r Repo) {
	ctx := context.Background()
	if _, ok, err := r.Licence(ctx); ok || err != nil {
		t.Fatalf("licence before save: %v %v", ok, err)
	}
	if _, ok, err := r.EngineFetch(ctx); ok || err != nil {
		t.Fatalf("fetch before save: %v %v", ok, err)
	}
	l := domain.Licence{Version: "v1", AcceptedBy: "ada", AcceptedAt: t0}
	must(t, r.SaveLicence(ctx, l))
	f := domain.EngineFetch{State: "downloading", Progress: 0.5, BytesDone: 5, BytesTotal: 10, UpdatedAt: t0}
	must(t, r.SaveEngineFetch(ctx, f))
	f.State = "ready"
	must(t, r.SaveEngineFetch(ctx, f))
	gotL, _, err := r.Licence(ctx)
	must(t, err)
	gotF, _, err := r.EngineFetch(ctx)
	must(t, err)
	if diff := cmp.Diff(l, gotL); diff != "" {
		t.Fatalf("licence (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(f, gotF); diff != "" {
		t.Fatalf("fetch (-want +got):\n%s", diff)
	}
}
