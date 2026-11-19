// SPDX-License-Identifier: MIT

package gatewaymint

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/SDK-E/agentsone/internal/core"
)

// DefaultKeyFile is where the edge's read-only volume puts the key.
const DefaultKeyFile = "/run/agentsone/gateway-key/key"

// Settings is the [gateway] table of the daemon config.
type Settings struct {
	// KeyFile holds the raw gateway key.
	KeyFile string `toml:"key_file"`
}

// Module provides core.GatewayTokens.
type Module struct {
	core.Base
	minter *Minter
	log    *slog.Logger
	err    error
}

var _ core.Module = (*Module)(nil)

// New builds the module from the [gateway] settings. A bad table fails
// Start, so the daemon does not run with a key path it did not mean.
func New(deps core.Deps) *Module {
	s := Settings{KeyFile: DefaultKeyFile}
	var err error
	if deps.Config != nil {
		err = deps.Config.Section("gateway", &s)
	}
	if err == nil && s.KeyFile == "" {
		err = errors.New("[gateway] key_file is empty")
	}
	return &Module{minter: &Minter{KeyFile: s.KeyFile, Clock: deps.Clock, Ports: deps.Ports}, log: deps.Log, err: err}
}

// Name is the module name.
func (*Module) Name() string { return "gateway" }

// Provide makes the minter the daemon's gateway token port.
func (m *Module) Provide(p *core.Ports) { p.GatewayTokens = m.minter }

// Start fails on bad settings. A missing key does not stop the daemon:
// the edge may mount it later, and until then each mint says the model
// gateway is not reachable.
func (m *Module) Start(context.Context) error {
	if m.err != nil {
		return fmt.Errorf("gateway settings: %w", m.err)
	}
	if _, err := m.minter.Key(); err != nil && m.log != nil {
		m.log.Warn(err.Error())
	}
	return nil
}
