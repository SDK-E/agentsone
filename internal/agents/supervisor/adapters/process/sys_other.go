// SPDX-License-Identifier: MIT

//go:build !linux

package process

import (
	"errors"
	"fmt"
	"os/exec"

	"github.com/SDK-E/agentsone/internal/agents/supervisor/domain"
	"github.com/SDK-E/agentsone/internal/core"
)

// sysState is empty off Linux: the daemon only runs agents on Linux, and
// other systems run runners unconfined for development.
type sysState struct{ mode domain.LimitMode }

func prepare(_ *exec.Cmd, spec domain.UnitSpec, _ string) (*sysState, error) {
	if spec.Account.IsRoot || spec.Limits == (domain.Limits{}) {
		return &sysState{mode: domain.LimitsNone}, nil
	}
	return &sysState{mode: domain.LimitsSoft}, nil
}

func (st *sysState) afterStart() (domain.LimitMode, error) { return st.mode, nil }

func (*sysState) cleanup() {}

// cellFor places nothing off Linux, like prepare.
func cellFor(string, domain.UnitSpec) core.ProcessCell { return core.ProcessCell{} }

// errNoCells is any placement off Linux.
var errNoCells = fmt.Errorf("cgroups and the launch helper need Linux: %w", errors.ErrUnsupported)

func humanCgroup(string, string) (string, error) { return "", errNoCells }

// Placement is how the launch helper sets up a program; Linux only.
type Placement struct {
	Cell     core.ProcessCell
	UID, GID int
}

// Placed is a command started through the launch helper; Linux only.
type Placed struct{}

// Place fails off Linux: there is no launch helper.
func Place(*exec.Cmd, Placement) (*Placed, error) { return nil, errNoCells }

// Started never runs off Linux.
func (*Placed) Started() error { return errNoCells }

// Close has nothing to close off Linux.
func (*Placed) Close() {}
