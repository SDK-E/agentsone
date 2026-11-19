// SPDX-License-Identifier: MIT

package system

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/SDK-E/agentsone/internal/machine/app"
	"github.com/SDK-E/agentsone/internal/machine/domain"
)

// Runner runs a command and returns its combined output.
type Runner interface {
	Run(ctx context.Context, name string, args ...string) ([]byte, error)
}

// ExecRunner runs real commands without a shell, non-interactively. apt
// reaches the mirrors through the edge egress proxy set in the image.
type ExecRunner struct{}

// Run runs name with args.
func (ExecRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec // G204: fixed programs, no shell; Apt passes only names and versions the domain checked.
	cmd.Env = append(os.Environ(), "DEBIAN_FRONTEND=noninteractive")
	return cmd.CombinedOutput()
}

// Apt is the package manager on dpkg and apt-get. Names and versions are
// checked by the domain before they reach here, and "--" ends the options.
type Apt struct{ run Runner }

var _ app.PackageManager = Apt{}

// NewApt returns the package manager over run.
func NewApt(run Runner) Apt { return Apt{run: run} }

const dpkgFormat = `${db:Status-Abbrev}\t${Package}\t${Version}\t${Architecture}\t${binary:Summary}\n`

// Installed lists packages dpkg reports as installed.
func (a Apt) Installed(ctx context.Context) ([]domain.Package, error) {
	out, err := a.run.Run(ctx, "dpkg-query", "-W", "-f="+dpkgFormat)
	if err != nil {
		return nil, fmt.Errorf("dpkg-query: %w: %s", err, strings.TrimSpace(string(out)))
	}
	var pkgs []domain.Package
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Split(line, "\t")
		if len(f) != 5 || !strings.HasPrefix(f[0], "ii") {
			continue
		}
		pkgs = append(pkgs, domain.Package{Name: f[1], Version: f[2], Architecture: f[3], Summary: f[4]})
	}
	return pkgs, nil
}

// Manual lists packages marked manually installed.
func (a Apt) Manual(ctx context.Context) ([]string, error) {
	out, err := a.run.Run(ctx, "apt-mark", "showmanual")
	if err != nil {
		return nil, fmt.Errorf("apt-mark showmanual: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return strings.Fields(string(out)), nil
}

// Install installs name, pinned to version when given.
func (a Apt) Install(ctx context.Context, name, version string) error {
	target := name
	if version != "" {
		target += "=" + version
	}
	return a.apt(ctx, "install", "-y", "--no-install-recommends", "--", target)
}

// Remove removes name.
func (a Apt) Remove(ctx context.Context, name string) error {
	return a.apt(ctx, "remove", "-y", "--", name)
}

func (a Apt) apt(ctx context.Context, args ...string) error {
	out, err := a.run.Run(ctx, "apt-get", args...)
	if err != nil {
		return fmt.Errorf("apt-get %s: %w: %s", args[0], err, lastLine(out))
	}
	return nil
}

func lastLine(out []byte) string {
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	return lines[len(lines)-1]
}
