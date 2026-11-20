// SPDX-License-Identifier: MIT

package desktop_test

import (
	"context"
	"errors"
	"testing"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/core/coretest"
	"github.com/SDK-E/agentsone/internal/desktop"
	"github.com/SDK-E/agentsone/internal/humanuser/domain"
)

type madeUsers struct{ made []domain.User }

func (m *madeUsers) Ensure(_ context.Context, u domain.User) error {
	m.made = append(m.made, u)
	return nil
}

func TestTheDesktopRunsAsTheHumansOwnLinuxUser(t *testing.T) {
	deps, fakes := coretest.NewDeps(t)
	fakes.Humans.Add("ada", 1000)
	made := &madeUsers{}
	acc := desktop.HumanAccounts(deps.Ports, made)
	u, err := acc.HumanUser(context.Background(), "ada")
	if err != nil {
		t.Fatal(err)
	}
	if u.Name != "h_ada" || u.UID != 1000 || u.GID != 1000 || u.Home != "/home/humans/ada" {
		t.Errorf("user %+v", u)
	}
	if len(made.made) != 1 {
		t.Errorf("made %d users, want 1 (the user is made again if /etc was reset)", len(made.made))
	}
	if _, err := acc.HumanUser(context.Background(), "ghost"); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("unknown human: %v, want ErrNotFound", err)
	}
}
