// SPDX-License-Identifier: MIT

// Package edgeenv is what every edge package receives from cmd/agentsone.
//
// The edge runs in a container of the Docker compose stack, so its folders are fixed container
// paths. It never guesses a home folder, XDG folder or macOS folder.
//
// Owned by work package 0 (contract). Frozen: see contract/FREEZE.md.
package edgeenv

import (
	"errors"
	"fmt"
	"io"
	"log/slog"

	"github.com/spf13/cobra"
)

const (
	// DataDir is where the edge keeps its state inside the container (a volume).
	DataDir = "/var/lib/agentsone-edge"
	// ConfigDir is where the edge reads its configuration inside the container.
	ConfigDir = "/etc/agentsone-edge"
)

// Env is shared by edge commands. Edge packages export
//
//	func Commands(env *edgeenv.Env) []*cobra.Command
//
// and cmd/agentsone registers what they return on the root command.
type Env struct {
	Out io.Writer
	Err io.Writer
	Log *slog.Logger
	// DataDir is the state folder, /var/lib/agentsone-edge.
	DataDir string
	// ConfigDir is the configuration folder, /etc/agentsone-edge.
	ConfigDir string
}

// New builds the environment of the edge container.
func New(out, errOut io.Writer) *Env {
	return &Env{
		Out:       out,
		Err:       errOut,
		Log:       slog.New(slog.NewTextHandler(errOut, &slog.HandlerOptions{Level: slog.LevelInfo})),
		DataDir:   DataDir,
		ConfigDir: ConfigDir,
	}
}

// ErrNotBuilt is returned by commands whose work package has not landed.
var ErrNotBuilt = errors.New("not built yet")

// NotBuilt is a placeholder command body that names the owning work package.
func NotBuilt(workPackage string) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, _ []string) error {
		return fmt.Errorf("%s: %w (work package %s)", cmd.CommandPath(), ErrNotBuilt, workPackage)
	}
}
