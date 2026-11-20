// SPDX-License-Identifier: MIT

// Command genlicence writes the engine licence file the machine image ships,
// /usr/lib/agentsone/engine/licence.json, from the pinned engine and the one
// licence notice in internal/enginefetch/domain. The image build runs it, so
// the engine name and version are never typed a second time.
//
// Usage: genlicence <file>
package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/SDK-E/agentsone/internal/enginefetch/domain"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stderr))
}

// run writes the licence file named by args and returns the exit code.
func run(args []string, stderr io.Writer) int {
	if len(args) != 1 {
		_, _ = fmt.Fprintln(stderr, "usage: genlicence <file>") //nolint:errcheck // stderr is the last place to report; the exit code still says failure
		return 2
	}
	if err := write(args[0]); err != nil {
		_, _ = fmt.Fprintf(stderr, "error: %v\n", err) //nolint:errcheck // stderr is the last place to report; the exit code still says failure
		return 1
	}
	return 0
}

func write(path string) error {
	pin, err := domain.Pinned()
	if err != nil {
		return err
	}
	b, err := pin.LicenceJSON()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil { //nolint:gosec // image folder, read by every agent user
		return fmt.Errorf("create licence folder: %w", err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil { //nolint:gosec // public notice text, no secret
		return fmt.Errorf("write licence file: %w", err)
	}
	return nil
}
