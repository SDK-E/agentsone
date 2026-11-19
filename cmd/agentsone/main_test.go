// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/SDK-E/agentsone/internal/edge/edgeenv"
)

func testEnv(errOut *bytes.Buffer) *edgeenv.Env {
	return &edgeenv.Env{Out: &bytes.Buffer{}, Err: errOut}
}

func TestRootRegistersEveryEdgeCommand(t *testing.T) {
	root := newRoot(testEnv(&bytes.Buffer{}))
	for _, name := range []string{"edge", "control", "net", "gateway", "budget", "audit"} {
		cmd, _, err := root.Find([]string{name})
		if err != nil || cmd.Name() != name {
			t.Errorf("%s not registered: %v", name, err)
		}
	}
}

// The web door is a part of `agentsone edge`, opened by --door-addr; there is nothing to run apart.
func TestRootHasNoDoorCommand(t *testing.T) {
	root := newRoot(testEnv(&bytes.Buffer{}))
	if cmd, _, err := root.Find([]string{"door"}); err == nil && cmd.Name() == "door" {
		t.Fatal("door is registered as a command")
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

func TestEdgeRefusesToStartWithoutItsInnerAddress(t *testing.T) {
	root := newRoot(testEnv(&bytes.Buffer{}))
	root.SetArgs([]string{"edge"})
	if err := root.Execute(); err == nil || !strings.Contains(err.Error(), "--inner-addr") {
		t.Fatalf("edge: got %v, want an error naming --inner-addr", err)
	}
}

func TestControlResetIsACommand(t *testing.T) {
	root := newRoot(testEnv(&bytes.Buffer{}))
	cmd, _, err := root.Find([]string{"control", "reset"})
	if err != nil || cmd.Name() != "reset" {
		t.Fatalf("control reset not registered: %v", err)
	}
}
