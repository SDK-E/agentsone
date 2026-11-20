// SPDX-License-Identifier: MIT

package desktop

import (
	"context"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/desktop/app"
	humanapp "github.com/SDK-E/agentsone/internal/humanuser/app"
)

// HumanAccounts finds each human's own Linux user through the Humans port
// (read when used: ports are filled after New), making it first when the
// container's /etc was reset.
func HumanAccounts(ports *core.Ports, accounts humanapp.Accounts) app.Accounts {
	return humanAccounts{users: humanapp.New(lateHumans{ports}, accounts)}
}

type humanAccounts struct{ users *humanapp.Users }

func (h humanAccounts) HumanUser(ctx context.Context, human string) (app.User, error) {
	u, err := h.users.Ensure(ctx, human)
	if err != nil {
		return app.User{}, err // Ensure names the human already
	}
	return app.User{Name: u.Name, UID: u.UID, GID: u.GID, Home: u.Home}, nil
}

// lateHumans reads the Humans port when used.
type lateHumans struct{ p *core.Ports }

func (l lateHumans) HumanAccounts(ctx context.Context) ([]core.HumanAccount, error) {
	return l.p.Humans.HumanAccounts(ctx)
}

func (l lateHumans) HumanAccount(ctx context.Context, name string) (core.HumanAccount, error) {
	return l.p.Humans.HumanAccount(ctx, name)
}
