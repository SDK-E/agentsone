// SPDX-License-Identifier: MIT

package edgeenv_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/SDK-E/agentsone/internal/edge/edgeenv"
)

func TestNewUsesContainerPaths(t *testing.T) {
	env := edgeenv.New(&bytes.Buffer{}, &bytes.Buffer{})
	if env.DataDir != "/var/lib/agentsone-edge" {
		t.Errorf("DataDir = %q", env.DataDir)
	}
	if env.ConfigDir != "/etc/agentsone-edge" {
		t.Errorf("ConfigDir = %q", env.ConfigDir)
	}
}

func TestNewIgnoresHostEnvironment(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "/somewhere/else")
	t.Setenv("HOME", "/Users/someone")
	env := edgeenv.New(&bytes.Buffer{}, &bytes.Buffer{})
	if strings.Contains(env.DataDir, "somewhere") || strings.Contains(env.DataDir, "Users") {
		t.Fatalf("DataDir follows the host environment: %q", env.DataDir)
	}
}

func TestNewLogsToTheErrorWriter(t *testing.T) {
	var errOut bytes.Buffer
	env := edgeenv.New(&bytes.Buffer{}, &errOut)
	env.Log.Info("hello")
	if !strings.Contains(errOut.String(), "hello") {
		t.Fatalf("log not written: %q", errOut.String())
	}
}

func TestNotBuiltNamesTheCommandAndWorkPackage(t *testing.T) {
	cmd := &cobra.Command{Use: "door"}
	err := edgeenv.NotBuilt("16")(cmd, nil)
	if !errors.Is(err, edgeenv.ErrNotBuilt) {
		t.Fatalf("got %v", err)
	}
	if !strings.Contains(err.Error(), "door") || !strings.Contains(err.Error(), "16") {
		t.Fatalf("message %q", err)
	}
}
