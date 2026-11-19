// SPDX-License-Identifier: MIT

//go:build linux && integration

package system_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/SDK-E/agentsone/internal/machine/adapters/system"
	"github.com/SDK-E/agentsone/internal/machine/domain"
)

// TestPtyRunsAsTheAgentUser needs root, as the daemon has in the machine
// container: a custom agent's terminal must run as that agent's uid, with
// no group left over from the daemon, in the agent's own home.
func TestPtyRunsAsTheAgentUser(t *testing.T) {
	if os.Getuid() != 0 {
		t.Fatal("run the system tests as root in a throwaway container")
	}
	const agent = 200201
	home := agentHome(t, agent)
	spec := domain.PtySpec{Command: []string{"/bin/sh", "-c", "echo uid=$(id -u) gid=$(id -g) groups=$(id -G) dir=$(pwd)"}, Cols: 80, Rows: 24}
	p, err := system.PtyStarter{}.Start(ctx(), spec, agent, agent, home)
	if err != nil {
		t.Fatal(err)
	}
	readUntil(t, p, fmt.Sprintf("uid=%d gid=%d groups=%d dir=%s", agent, agent, agent, home))
	if code := p.Wait(); code != 0 {
		t.Fatalf("exit code %d", code)
	}
	_ = p.Close()
}

// agentHome lays out a home the way the supervisor does on the machine: a
// private 0700 directory owned by the agent under a root-owned 0755 homes
// root, like /home/agents/<name>. t.TempDir does not fit: it nests every
// directory inside a 0700 one that only root can cross, so the agent cannot
// enter its home and the start fails with EACCES.
func agentHome(t *testing.T, uid int) string {
	t.Helper()
	homes, err := os.MkdirTemp("", "homes")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(homes) })
	if err := os.Chmod(homes, 0o755); err != nil { // #nosec G302 -- like /home/agents: every agent crosses it to reach its own home
		t.Fatal(err)
	}
	home := filepath.Join(homes, "agent")
	if err := os.Mkdir(home, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(home, uid, uid); err != nil {
		t.Fatal(err)
	}
	return home
}
