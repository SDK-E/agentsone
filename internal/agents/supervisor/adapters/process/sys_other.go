// SPDX-License-Identifier: MIT

//go:build !linux

package process

import (
	"os/exec"

	"github.com/SDK-E/agentsone/internal/agents/supervisor/domain"
)

// sysState is empty off Linux: the daemon only runs agents on Linux, and
// other systems run runners unconfined for development.
type sysState struct{ isRoot bool }

func prepare(_ *exec.Cmd, spec domain.UnitSpec, _ string) (*sysState, error) {
	return &sysState{isRoot: spec.Account.IsRoot}, nil
}

func (st *sysState) afterStart(_ int, l domain.Limits) (domain.LimitMode, error) {
	if st.isRoot || l == (domain.Limits{}) {
		return domain.LimitsNone, nil
	}
	return domain.LimitsSoft, nil
}

func (*sysState) cleanup() {}
