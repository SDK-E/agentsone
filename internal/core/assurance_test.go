// SPDX-License-Identifier: MIT

package core_test

import (
	"testing"

	"github.com/SDK-E/agentsone/internal/core"
)

func TestCaller_AssuranceIsHardByDefault(t *testing.T) {
	t.Parallel()
	if (core.Caller{Kind: core.CallerHuman, Name: "ada"}).Assurance != core.AssuranceHard {
		t.Fatal("zero assurance is not hard")
	}
	if core.AssuranceSoft == core.AssuranceHard {
		t.Fatal("soft equals hard")
	}
}
