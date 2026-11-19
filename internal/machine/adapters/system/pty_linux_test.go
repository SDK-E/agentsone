// SPDX-License-Identifier: MIT

//go:build linux && integration

package system_test

import (
	"os"
	"testing"

	"github.com/SDK-E/agentsone/internal/machine/adapters/system"
	"github.com/SDK-E/agentsone/internal/machine/domain"
)

// TestPtyRunsAsTheAgentUser needs root, as the daemon has in the machine
// container: a custom agent's terminal must run as that agent's uid.
func TestPtyRunsAsTheAgentUser(t *testing.T) {
	if os.Getuid() != 0 {
		t.Fatal("run the system tests as root in a throwaway container")
	}
	const agent = 200201
	home := t.TempDir()
	if err := os.Chown(home, agent, agent); err != nil {
		t.Fatal(err)
	}
	spec := domain.PtySpec{Command: []string{"/bin/sh", "-c", "echo uid=$(id -u) gid=$(id -g)"}, Cols: 80, Rows: 24}
	p, err := system.PtyStarter{}.Start(ctx(), spec, agent, agent, home)
	if err != nil {
		t.Fatal(err)
	}
	readUntil(t, p, "uid=200201 gid=200201")
	if code := p.Wait(); code != 0 {
		t.Fatalf("exit code %d", code)
	}
	_ = p.Close()
}
