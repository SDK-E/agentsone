// SPDX-License-Identifier: MIT

//go:build !linux

package process

import (
	"os/exec"

	"github.com/SDK-E/agentsone/internal/agents/supervisor/domain"
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
