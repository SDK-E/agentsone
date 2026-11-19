// SPDX-License-Identifier: MIT

// Command agentsone-runner-echo is the model-free runner. It is installed as
// /usr/lib/agentsone/runners/echo.
package main

import (
	"os"

	"github.com/SDK-E/agentsone/runners/echo"
	"github.com/SDK-E/agentsone/runners/sdk"
)

func main() {
	os.Exit(sdk.Main(echo.Options(), echo.New()))
}
