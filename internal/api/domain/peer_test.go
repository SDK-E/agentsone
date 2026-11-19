// SPDX-License-Identifier: MIT

package domain_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/SDK-E/agentsone/internal/api/domain"
	"github.com/SDK-E/agentsone/internal/core"
)

func TestAgentFromCgroup_ReadsTheAgentCgroup(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		cgroup string
		want   string
		ok     bool
	}{
		{"agent cgroup", "0::/agents/founder\n", "founder", true},
		{"tool spawned by the runner", "0::/agents/ops-2/tools/git\n", "ops-2", true},
		{"cgroup v1 lines are ignored", "12:pids:/agents/ceo\n0::/", "", false},
		{"the daemon", "0::/daemon", "", false},
		{"agents root itself", "0::/agents", "", false},
		{"invalid agent name", "0::/agents/CEO", "", false},
		{"nested elsewhere", "0::/daemon/agents/ceo", "", false},
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

func agents(m map[int]string) domain.AgentByUID {
	return func(uid int) (string, bool) {
		n, ok := m[uid]
		return n, ok
	}
}

func TestResolvePeer_NamesTheCaller(t *testing.T) {
	t.Parallel()
	known := agents(map[int]string{200001: "scout"})
	cases := []struct {
		name   string
		peer   domain.Peer
		cgroup string
		want   core.Caller
	}{
		{
			"hard: root agent by its cgroup", domain.Peer{PID: 10, UID: 0}, "0::/agents/founder",
			core.Caller{Kind: core.CallerAgent, Name: "founder", UID: 0, IsRoot: true},
		},
		{
			"hard: two uid-0 agents stay distinct", domain.Peer{PID: 11, UID: 0}, "0::/agents/cto",
			core.Caller{Kind: core.CallerAgent, Name: "cto", UID: 0, IsRoot: true},
		},
		{
			"hard: custom agent by its cgroup", domain.Peer{PID: 12, UID: 200001}, "0::/agents/scout/tools",
			core.Caller{Kind: core.CallerAgent, Name: "scout", UID: 200001},
		},
		{
			"soft: custom agent by uid when cgroups are read only", domain.Peer{PID: 13, UID: 200001}, "0::/",
			core.Caller{Kind: core.CallerAgent, Name: "scout", UID: 200001},
		},
		{
			"soft: unknown uid in the agent range is system", domain.Peer{PID: 14, UID: 200002}, "0::/",
			core.Caller{Kind: core.CallerSystem, Name: "uid:200002", UID: 200002},
		},
		{
			"uid outside the agent range is never an agent", domain.Peer{PID: 15, UID: 1000}, "0::/",
			core.Caller{Kind: core.CallerSystem, Name: "uid:1000", UID: 1000},
		},
		{
			"uid 0 without an agent cgroup is system, never an agent", domain.Peer{PID: 16, UID: 0}, "0::/",
			core.Caller{Kind: core.CallerSystem, Name: "uid:0", UID: 0, IsRoot: true},
		},
		{
			"the daemon's own cgroup", domain.Peer{PID: 17, UID: 0}, "0::/daemon",
			core.Caller{Kind: core.CallerSystem, Name: "daemon", UID: 0, IsRoot: true},
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
