// SPDX-License-Identifier: MIT

package app_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/humanuser/app"
	"github.com/SDK-E/agentsone/internal/humanuser/domain"
)

type directory struct {
	humans []core.HumanAccount
	err    error
}

func (d directory) HumanAccounts(context.Context) ([]core.HumanAccount, error) {
	return d.humans, d.err
}

func (d directory) HumanAccount(_ context.Context, name string) (core.HumanAccount, error) {
	if d.err != nil {
		return core.HumanAccount{}, d.err
	}
	for _, h := range d.humans {
		if h.Name == name {
			return h, nil
		}
	}
	return core.HumanAccount{}, fmt.Errorf("human %s: %w", name, core.ErrNotFound)
}

func (d directory) HumanByUID(_ context.Context, uid int) (core.HumanAccount, error) {
	return core.HumanAccount{}, fmt.Errorf("users never look humans up by uid %d: %w", uid, core.ErrNotFound)
}

type accounts struct {
	mu      sync.Mutex
	ensured []string
	fail    map[string]error
}

func (a *accounts) Ensure(_ context.Context, u domain.User) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.fail[u.Human]; err != nil {
		return err
	}
	a.ensured = append(a.ensured, u.Name)
	return nil
}

var humans = directory{humans: []core.HumanAccount{{Name: "ada", UID: 1000}, {Name: "bob", UID: 1001}}}

func TestEnsureMakesTheHumansUser(t *testing.T) {
	acc := &accounts{}
	u, err := app.New(humans, acc).Ensure(context.Background(), "bob")
	if err != nil {
		t.Fatal(err)
	}
	want, _ := domain.UserFor("bob", 1001)
	if u != want || !reflect.DeepEqual(acc.ensured, []string{"h_bob"}) {
		t.Fatalf("user %+v ensured %q", u, acc.ensured)
	}
}

func TestEnsureRefusesAnUnknownHuman(t *testing.T) {
	acc := &accounts{}
	if _, err := app.New(humans, acc).Ensure(context.Background(), "eve"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("unknown human: %v", err)
	}
	if len(acc.ensured) != 0 {
		t.Fatalf("ensured %q", acc.ensured)
	}
}

func TestEnsureRefusesAHumanWithoutAValidUser(t *testing.T) {
	odd := directory{humans: []core.HumanAccount{{Name: "ada", UID: 70000}}}
	if _, err := app.New(odd, &accounts{}).Ensure(context.Background(), "ada"); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("uid outside the range: %v", err)
	}
}

func TestEnsureReportsAFailedAccount(t *testing.T) {
	boom := errors.New("useradd failed")
	acc := &accounts{fail: map[string]error{"ada": boom}}
	if _, err := app.New(humans, acc).Ensure(context.Background(), "ada"); !errors.Is(err, boom) {
		t.Fatalf("error %v", err)
	}
}

func TestReconcileMakesEveryHumanAndGoesOnPastAFailure(t *testing.T) {
	boom := errors.New("useradd failed")
	acc := &accounts{fail: map[string]error{"ada": boom}}
	n, err := app.New(humans, acc).Reconcile(context.Background())
	if n != 1 || !errors.Is(err, boom) {
		t.Fatalf("made %d, error %v", n, err)
	}
	if !reflect.DeepEqual(acc.ensured, []string{"h_bob"}) {
		t.Fatalf("ensured %q, want bob despite ada failing", acc.ensured)
	}
}

func TestReconcileReportsAnUnreadableDirectory(t *testing.T) {
	down := directory{err: core.ErrUnavailable}
	if _, err := app.New(down, &accounts{}).Reconcile(context.Background()); !errors.Is(err, core.ErrUnavailable) {
		t.Fatalf("error %v", err)
	}
}

func TestEnsureIsSafeFromManyGoroutines(t *testing.T) {
	acc := &accounts{}
	users := app.New(humans, acc)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if _, err := users.Ensure(context.Background(), "ada"); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if len(acc.ensured) != 8 {
		t.Fatalf("ensured %d times", len(acc.ensured))
	}
}
