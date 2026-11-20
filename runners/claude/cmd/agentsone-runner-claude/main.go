// SPDX-License-Identifier: MIT

// Command agentsone-runner-claude is the Claude runner. The daemon starts it
// with AGENTSONE_RUNNER_SOCKET set. It drives the Claude Code engine that
// the machine downloads after a human accepts its licence.
package main

import (
	"os"

	"github.com/SDK-E/agentsone/runners/claude"
	"github.com/SDK-E/agentsone/runners/sdk"
)

func main() {
	r := claude.New(claude.Config{})
	code := sdk.Main(claude.Options(), r)
	_ = r.Close() // stops the engine; the process ends next, so a close error has nowhere to go
	os.Exit(code)
}
