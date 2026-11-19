// SPDX-License-Identifier: MIT

// Command agentsone-runner-openai is the OpenAI-compatible runner. The daemon
// starts it with AGENTSONE_RUNNER_SOCKET set.
package main

import (
	"os"

	"github.com/SDK-E/agentsone/runners/openai"
	"github.com/SDK-E/agentsone/runners/sdk"
)

func main() {
	r := openai.New(openai.Config{})
	code := sdk.Main(openai.Options(), r)
	_ = r.Close() // the process ends next; a close error has nowhere to go
	os.Exit(code)
}
