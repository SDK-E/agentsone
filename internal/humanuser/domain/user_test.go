// SPDX-License-Identifier: MIT

package domain_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/humanuser/domain"
)

func TestUserForGivesTheHumanAUserNoAgentCanHave(t *testing.T) {
	u, err := domain.UserFor("ada", 1000)
	if err != nil {
		t.Fatal(err)
	}
	want := domain.User{Human: "ada", Name: "h_ada", UID: 1000, GID: 1000, Home: "/home/humans/ada", Shell: "/bin/bash"}
	if u != want {
		t.Fatalf("user %+v, want %+v", u, want)
	}
}

func TestUserForAcceptsTheLongestNameThatFitsALinuxUserName(t *testing.T) {
	name := "a" + strings.Repeat("b", domain.MaxHumanLen-1)
	u, err := domain.UserFor(name, 59999)
	if err != nil {
		t.Fatal(err)
	}
	if len(u.Name) != 32 {
		t.Fatalf("user name %q is %d bytes, want 32", u.Name, len(u.Name))
	}
}

func TestUserForRefusesBadNames(t *testing.T) {
	for _, name := range []string{
		"", "Ada", "1ada", "-ada", "ada_x", "ada.x", "ada/x", "../x", "ada x",
		"a" + strings.Repeat("b", domain.MaxHumanLen),
	} {
		if _, err := domain.UserFor(name, 1000); !errors.Is(err, core.ErrInvalid) {
			t.Errorf("%q: %v, want ErrInvalid", name, err)
		}
	}
}

func TestUserForRefusesUIDsOutsideTheHumanRange(t *testing.T) {
	for _, uid := range []int{-1, 0, 999, 60000, 200000} {
		if _, err := domain.UserFor("ada", uid); !errors.Is(err, core.ErrInvalid) {
			t.Errorf("uid %d: %v, want ErrInvalid", uid, err)
		}
	}
}
