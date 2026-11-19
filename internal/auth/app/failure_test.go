// SPDX-License-Identifier: MIT

package app_test

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/auth/adapters/memory"
	"github.com/SDK-E/agentsone/internal/auth/app"
	"github.com/SDK-E/agentsone/internal/auth/authtest"
	"github.com/SDK-E/agentsone/internal/auth/domain"
	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/core/coretest"
)

var errDisk = errors.New("disk I/O error")

// broken is a store whose every read and write fails.
type broken struct{ *memory.Store }

func (broken) ListHumans(context.Context) ([]domain.Human, error) { return nil, errDisk }
func (broken) Human(context.Context, string) (domain.Human, error) {
	return domain.Human{}, errDisk
}
func (broken) SaveToken(context.Context, domain.Token) error       { return errDisk }
func (broken) Token(context.Context, string) (domain.Token, error) { return domain.Token{}, errDisk }
func (broken) Session(context.Context, string) (domain.Session, error) {
	return domain.Session{}, errDisk
}
func (broken) Licence(context.Context) (domain.Licence, bool, error) {
	return domain.Licence{}, false, errDisk
}
func (broken) EngineFetch(context.Context) (domain.EngineFetch, bool, error) {
	return domain.EngineFetch{}, false, errDisk
}

func brokenService(t *testing.T, licence app.LicenceSource) *app.Service {
	t.Helper()
	clock := coretest.NewClock(coretest.Epoch)
	b := broken{memory.New()}
	svc, err := app.New(app.Deps{
		Clock: clock, Rand: &authtest.Counter{}, Argon2: authtest.Cheap, Sessions: b, Tokens: b, Humans: b, Setup: b,
		Licence: licence, Events: coretest.NewEvents(clock, coretest.NewIDs()), Audit: coretest.NewAudit(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

func TestNew_FailsWhenRandomnessFails(t *testing.T) {
	t.Parallel()
	if _, err := app.New(app.Deps{Rand: bytes.NewReader(nil), Argon2: authtest.Cheap}); err == nil {
		t.Fatal("no error")
	}
}

func TestService_ReportsStorageFailures(t *testing.T) {
	t.Parallel()
	svc := brokenService(t, authtest.DefaultLicence)
	ctx := context.Background()
	checks := map[string]error{}
	_, checks["ListHumans"] = svc.ListHumans(ctx)
	_, checks["SetupStatus"] = svc.SetupStatus(ctx)
	_, checks["CreateFirstHuman"] = svc.CreateFirstHuman(ctx, app.NewHuman{Name: "ada", Password: password})
	_, checks["CreateHuman"] = svc.CreateHuman(ctx, authtest.Human("ada"), app.NewHuman{Name: "bob", Password: password})
	checks["DeleteHuman"] = svc.DeleteHuman(ctx, authtest.Human("ada"), "bob")
	_, checks["EngineLicence"] = svc.EngineLicence(ctx)
	_, checks["EngineFetch"] = svc.EngineFetch(ctx)
	_, checks["Issue"] = svc.Issue(ctx, authtest.Agent("a"), nil, time.Hour)
	_, checks["Verify"] = svc.Verify(ctx, "aot_ab_cd")
	_, checks["SessionCaller"] = svc.SessionCaller(ctx, "aos_x")
	for name, err := range checks {
		if !errors.Is(err, errDisk) {
			t.Errorf("%s: err %v, want the storage error", name, err)
		}
	}
}

func TestEngineLicence_UnavailableWithoutALicenceFile(t *testing.T) {
	t.Parallel()
	svc := brokenService(t, authtest.Licence{})
	if _, err := svc.EngineLicence(context.Background()); !errors.Is(err, core.ErrUnavailable) {
		t.Fatalf("err %v", err)
	}
	if _, err := svc.AcceptEngineLicence(context.Background(), authtest.Human("ada"), "v1", true); !errors.Is(err, core.ErrUnavailable) {
		t.Fatalf("accept: err %v", err)
	}
}

func TestTooManyAttempts_SaysHowLongToWait(t *testing.T) {
	t.Parallel()
	err := &app.TooManyAttempts{RetryAfter: time.Minute}
	if err.Error() != "too many sign-in attempts: retry after 1m0s" {
		t.Fatalf("message %q", err.Error())
	}
}
