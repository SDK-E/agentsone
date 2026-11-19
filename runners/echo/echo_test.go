// SPDX-License-Identifier: MIT

package echo_test

import (
	"context"
	"strconv"
	"testing"

	"github.com/SDK-E/agentsone/runners/conformance"
	"github.com/SDK-E/agentsone/runners/echo"
	"github.com/SDK-E/agentsone/runners/sdk"
)

func subject() conformance.Subject {
	return conformance.Subject{
		Name: "echo",
		Start: conformance.InProcess(func(ctx context.Context, socket string) error {
			opts := echo.Options()
			opts.Socket = socket
			return sdk.Run(ctx, opts, echo.New())
		}),
		Prompts: conformance.Prompts{
			Plain:  "Hello echo",
			Hold:   echo.CmdHold,
			Budget: echo.CmdBudget,
			Long:   func(n int) string { return echo.CmdLong + " " + strconv.Itoa(n) },
		},
		SummaryInOutput: true,
	}
}

// TestConformance runs every rule of PROTOCOL.md against the echo runner
// (TESTING.md B4c).
func TestConformance(t *testing.T) {
	conformance.Run(t, subject())
}
