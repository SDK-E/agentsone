// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/SDK-E/agentsone/internal/edge/edgeenv"
)

func testEnv(errOut *bytes.Buffer) *edgeenv.Env {
	return &edgeenv.Env{Out: &bytes.Buffer{}, Err: errOut}
}

func TestRootRegistersEveryEdgeCommand(t *testing.T) {
	root := newRoot(testEnv(&bytes.Buffer{}))
	for _, name := range []string{"edge", "control", "net", "gateway", "budget", "audit", "door"} {
		cmd, _, err := root.Find([]string{name})
		if err != nil || cmd.Name() != name {
			t.Errorf("%s not registered: %v", name, err)
		}
	}
}

func TestRootHasNoVMCommands(t *testing.T) {
	root := newRoot(testEnv(&bytes.Buffer{}))
	for _, name := range []string{"up", "down", "status", "freeze", "rescue"} {
		if cmd, _, err := root.Find([]string{name}); err == nil && cmd.Name() == name {
			t.Errorf("%s is a VM command and must not exist", name)
		}
	}
}

func TestEdgeAndControlAreNotBuiltYet(t *testing.T) {
	for _, name := range []string{"edge", "control", "door"} {
		var errOut bytes.Buffer
		root := newRoot(testEnv(&errOut))
		root.SetArgs([]string{name})
		err := root.Execute()
		if !errors.Is(err, edgeenv.ErrNotBuilt) {
			t.Fatalf("%s: got %v", name, err)
		}
		if !strings.Contains(err.Error(), name) {
			t.Errorf("%s: error does not name the command: %v", name, err)
		}
	}
}
