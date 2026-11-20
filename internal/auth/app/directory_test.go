// SPDX-License-Identifier: MIT

package app_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/SDK-E/agentsone/internal/auth/authtest"
	"github.com/SDK-E/agentsone/internal/core"
)

func TestHumanAccounts_GiveEveryHumanWithTheirAccountNumber(t *testing.T) {
	t.Parallel()
	env := withHuman(t, "ada")
	ctx := context.Background()
	if _, err := env.Service.CreateHuman(ctx, authtest.Human("ada"), bob()); err != nil {
		t.Fatal(err)
	}
	var dir core.HumanDirectory = env.Service
	all, err := dir.HumanAccounts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := []core.HumanAccount{{Name: "ada", UID: 1000}, {Name: "bob", UID: 1001}}
	if !reflect.DeepEqual(all, want) {
		t.Fatalf("accounts %+v, want %+v", all, want)
	}
	one, err := dir.HumanAccount(ctx, "bob")
	if err != nil || one != want[1] {
		t.Fatalf("bob: %+v %v", one, err)
	}
	if _, err := dir.HumanAccount(ctx, "eve"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("unknown human: %v, want ErrNotFound", err)
	}
}
