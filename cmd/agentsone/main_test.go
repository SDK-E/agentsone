// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"errors"
	"testing"

	"github.com/SDK-E/agentsone/internal/host/hostenv"
)

func TestRootRegistersEveryHostCommand(t *testing.T) {
	root := newRoot(&hostenv.Env{Out: &bytes.Buffer{}, Err: &bytes.Buffer{}})
	for _, name := range []string{"up", "down", "status", "freeze", "rescue", "net", "gateway", "budget", "audit", "door", "control"} {
		cmd, _, err := root.Find([]string{name})
		if err != nil || cmd.Name() != name {
			t.Errorf("%s not registered: %v", name, err)
		}
	}
}

func TestUnbuiltCommandNamesItsWorkPackage(t *testing.T) {
	var errOut bytes.Buffer
	root := newRoot(&hostenv.Env{Out: &bytes.Buffer{}, Err: &errOut})
	root.SetArgs([]string{"door"})
	err := root.Execute()
	if !errors.Is(err, hostenv.ErrNotBuilt) {
		t.Fatalf("got %v", err)
	}
}
