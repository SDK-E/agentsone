// SPDX-License-Identifier: MIT

package domain_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/SDK-E/agentsone/internal/api/domain"
	"github.com/SDK-E/agentsone/internal/core"
)

func TestAgentFromCgroup_ReadsTheSystemAgentUnit(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		cgroup string
		want   string
		ok     bool
	}{
		{"template unit in its slice", "0::/system.slice/system-agent.slice/agent@founder.service\n", "founder", true},
		{"unit directly under system.slice", "0::/system.slice/agent@scout.service", "scout", true},
		{"delegated sub-cgroup of the unit", "0::/system.slice/system-agent.slice/agent@ops-2.service/runner\n", "ops-2", true},
		{"cgroup v1 lines are ignored", "12:pids:/system.slice/agent@ceo.service\n0::/user.slice/user-1000.slice/session-1.scope", "", false},
		{"user manager unit cannot pose as an agent", "0::/user.slice/user-1001.slice/user@1001.service/app.slice/agent@ceo.service", "", false},
		{"unit nested below another system unit", "0::/system.slice/sshd.service/agent@ceo.service", "", false},
		{"invalid agent name", "0::/system.slice/agent@CEO.service", "", false},
		{"empty instance", "0::/system.slice/agent@.service", "", false},
		{"other system unit", "0::/system.slice/agentsoned.service", "", false},
		{"empty file", "", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got, ok := domain.AgentFromCgroup(c.cgroup)
			if got != c.want || ok != c.ok {
				t.Fatalf("AgentFromCgroup(%q) = %q, %v; want %q, %v", c.cgroup, got, ok, c.want, c.ok)
			}
		})
	}
}

func TestSystemUnitFromCgroup_NamesTheUnit(t *testing.T) {
	t.Parallel()
	if got := domain.SystemUnitFromCgroup("0::/system.slice/agentsone-gateway.service\n"); got != "agentsone-gateway.service" {
		t.Fatalf("got %q", got)
	}
	if got := domain.SystemUnitFromCgroup("0::/user.slice/user-1000.slice/session-2.scope"); got != "" {
		t.Fatalf("got %q for a user session", got)
	}
}

func humans(m map[int]string) domain.HumanByUID {
	return func(uid int) (string, bool) {
		n, ok := m[uid]
		return n, ok
	}
}

func TestResolvePeer_NamesTheCaller(t *testing.T) {
	t.Parallel()
	known := humans(map[int]string{1000: "ada"})
	cases := []struct {
		name   string
		peer   domain.Peer
		cgroup string
		want   core.Caller
	}{
		{
			"root agent by its unit", domain.Peer{PID: 10, UID: 0},
			"0::/system.slice/system-agent.slice/agent@founder.service",
			core.Caller{Kind: core.CallerAgent, Name: "founder", UID: 0, IsRoot: true},
		},
		{
			"two uid-0 agents stay distinct", domain.Peer{PID: 11, UID: 0},
			"0::/system.slice/system-agent.slice/agent@cto.service",
			core.Caller{Kind: core.CallerAgent, Name: "cto", UID: 0, IsRoot: true},
		},
		{
			"custom agent with its own user", domain.Peer{PID: 12, UID: 2001},
			"0::/system.slice/system-agent.slice/agent@scout.service",
			core.Caller{Kind: core.CallerAgent, Name: "scout", UID: 2001},
		},
		{
			"human account by uid", domain.Peer{PID: 13, UID: 1000},
			"0::/user.slice/user-1000.slice/session-3.scope",
			core.Caller{Kind: core.CallerHuman, Name: "ada", UID: 1000},
		},
		{
			"human spoofing an agent unit in their user manager", domain.Peer{PID: 14, UID: 1000},
			"0::/user.slice/user-1000.slice/user@1000.service/app.slice/agent@founder.service",
			core.Caller{Kind: core.CallerHuman, Name: "ada", UID: 1000},
		},
		{
			"root system unit", domain.Peer{PID: 15, UID: 0},
			"0::/system.slice/agentsone-gateway.service",
			core.Caller{Kind: core.CallerSystem, Name: "agentsone-gateway.service", UID: 0, IsRoot: true},
		},
		{
			"unknown non-root user is system without root", domain.Peer{PID: 16, UID: 4242},
			"0::/user.slice/user-4242.slice/session-9.scope",
			core.Caller{Kind: core.CallerSystem, Name: "uid:4242", UID: 4242},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got := domain.ResolvePeer(c.peer, c.cgroup, known)
			if diff := cmp.Diff(c.want, got); diff != "" {
				t.Fatalf("ResolvePeer mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
