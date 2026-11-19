// SPDX-License-Identifier: MIT

package control

import (
	"context"

	"github.com/SDK-E/agentsone/internal/edge/component"
	"github.com/SDK-E/agentsone/internal/edge/control/adapters/credstore"
)

// Component is the edge side of the control page: it keeps the control passphrase hash in the
// edge data folder and serves it on the admin socket under /v1/control/. The control service
// itself has a read-only disk and no volume.
type Component struct{}

// Name names the component.
func (Component) Name() string { return "control credential" }

// Start adds the credential routes to the admin mux.
func (Component) Start(_ context.Context, env *component.Env) (component.Stopper, error) {
	credstore.Register(env.Admin, credstore.New(env.DataDir), env.Log)
	return stopper{}, nil
}

type stopper struct{}

func (stopper) Stop() error { return nil }
