// SPDX-License-Identifier: MIT

// Package hostenv is what every host package receives from cmd/agentsone.
//
// Owned by work package 0 (contract). Frozen: see contract/FREEZE.md.
package hostenv

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"

	"github.com/spf13/cobra"
)

// Env is shared by host commands. Host packages export
//
//	func Commands(env *hostenv.Env) []*cobra.Command
//
// and cmd/agentsone registers what they return on the root command.
type Env struct {
	Out io.Writer
	Err io.Writer
	Log *slog.Logger
	// DataDir is ~/Library/Application Support/agentsone on macOS.
	DataDir string
}

// New builds the environment for the current user.
func New(out, errOut io.Writer) (*Env, error) {
	dir, err := dataHome()
	if err != nil {
		return nil, err
	}
	return &Env{
		Out:     out,
		Err:     errOut,
		Log:     slog.New(slog.NewTextHandler(errOut, &slog.HandlerOptions{Level: slog.LevelInfo})),
		DataDir: filepath.Join(dir, "agentsone"),
	}, nil
}

// dataHome is ~/Library/Application Support on macOS and $XDG_DATA_HOME
// (default ~/.local/share) elsewhere, such as Linux CI.
func dataHome() (string, error) {
	if runtime.GOOS == "darwin" {
		dir, err := os.UserConfigDir()
		if err != nil {
			return "", fmt.Errorf("find Application Support: %w", err)
		}
		return dir, nil
	}
	if dir := os.Getenv("XDG_DATA_HOME"); dir != "" {
		return dir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("find the home folder: %w", err)
	}
	return filepath.Join(home, ".local", "share"), nil
}

// ErrNotBuilt is returned by commands whose work package has not landed.
var ErrNotBuilt = errors.New("not built yet")

// NotBuilt is a placeholder command body that names the owning work package.
func NotBuilt(workPackage string) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, _ []string) error {
		return fmt.Errorf("%s: %w (work package %s)", cmd.CommandPath(), ErrNotBuilt, workPackage)
	}
}
