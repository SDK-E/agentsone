// SPDX-License-Identifier: MIT

package domain_test

import (
	"context"
	"errors"
	"fmt"
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

// daemonPID is the daemon's own pid in these tests.
const daemonPID = 7

var errDown = errors.New("database is locked")

// lookup answers from m, ErrNotFound for any other uid, or err for every uid.
func lookup(m map[int]string, err error) func(context.Context, int) (string, error) {
	return func(_ context.Context, uid int) (string, error) {
		if err != nil {
			return "", err
		}
		if n, ok := m[uid]; ok {
			return n, nil
		}
		return "", fmt.Errorf("uid %d: %w", uid, core.ErrNotFound)
	}
}

// human answers nil for the names in m, ErrNotFound for any other name, or
// err for every name.
func human(m map[string]bool, err error) func(context.Context, string) error {
	return func(_ context.Context, name string) error {
		if err != nil {
			return err
		}
		if m[name] {
			return nil
		}
		return fmt.Errorf("human %s: %w", name, core.ErrNotFound)
	}
}

var known = domain.Directory{
	AgentByUID:  lookup(map[int]string{200001: "scout"}, nil),
	HumanByUID:  lookup(map[int]string{1000: "ada"}, nil),
	HumanByName: human(map[string]bool{"ada": true}, nil),
}

func TestHumanFromCgroup_ReadsTheHumanCgroup(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		cgroup string
		want   string
		under  bool
		ok     bool
	}{
		{"human terminal", "0::/humans/ada\n", "ada", true, true},
		{"program the terminal started", "0::/humans/ada/build", "ada", true, true},
		{"humans root itself", "0::/humans", "", true, false},
		{"invalid human name", "0::/humans/ADA", "", true, false},
		{"agent cgroup", "0::/agents/ada", "", false, false},
		{"nested elsewhere", "0::/daemon/humans/ada", "", false, false},
		{"cgroup v1 lines are ignored", "5:pids:/humans/ada\n0::/", "", false, false},
		{"empty file", "", "", false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got, under, ok := domain.HumanFromCgroup(c.cgroup)
			if got != c.want || under != c.under || ok != c.ok {
				t.Fatalf("HumanFromCgroup(%q) = %q, %v, %v; want %q, %v, %v", c.cgroup, got, under, ok, c.want, c.under, c.ok)
			}
		})
	}
}

func TestResolvePeer_NamesAnAgentAHumanOrTheDaemon(t *testing.T) {
	t.Parallel()
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
			core.Caller{Kind: core.CallerAgent, Name: "scout", UID: 200001, Assurance: core.AssuranceSoft},
		},
		{
			"human: the Linux user of a human is that human, not root", domain.Peer{PID: 14, UID: 1000}, "0::/daemon",
			core.Caller{Kind: core.CallerHuman, Name: "ada", UID: 1000, Assurance: core.AssuranceSoft},
		},
		{
			"soft: a custom agent's process outside its cgroup", domain.Peer{PID: 15, UID: 200001}, "0::/daemon",
			core.Caller{Kind: core.CallerAgent, Name: "scout", UID: 200001, Assurance: core.AssuranceSoft},
		},
		{
			"human cgroup: a human's root terminal is that human, never root", domain.Peer{PID: 23, UID: 0}, "0::/humans/ada",
			core.Caller{Kind: core.CallerHuman, Name: "ada", UID: 0, Assurance: core.AssuranceSoft},
		},
		{
			"human cgroup: a program the terminal started as another user", domain.Peer{PID: 24, UID: 200001}, "0::/humans/ada/make",
			core.Caller{Kind: core.CallerHuman, Name: "ada", UID: 200001, Assurance: core.AssuranceSoft},
		},
		{
			"agent cgroup first: a root process that moved into an agent's cgroup is that agent", domain.Peer{PID: 25, UID: 0}, "0::/agents/scout",
			core.Caller{Kind: core.CallerAgent, Name: "scout", UID: 0, IsRoot: true},
		},
		{
			"daemon: its own process", domain.Peer{PID: daemonPID, UID: 0}, "0::/daemon",
			core.Caller{Kind: core.CallerSystem, Name: "daemon", UID: 0, IsRoot: true},
		},
		{
			"daemon: its own process where it does not run as root", domain.Peer{PID: daemonPID, UID: 501}, "",
			core.Caller{Kind: core.CallerSystem, Name: "daemon", UID: 501},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got, err := domain.ResolvePeer(context.Background(), c.peer, c.cgroup, daemonPID, known)
			if err != nil {
				t.Fatalf("refused: %v", err)
			}
			if diff := cmp.Diff(c.want, got); diff != "" {
				t.Fatalf("ResolvePeer mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestResolvePeer_RefusesEveryOtherPeer(t *testing.T) {
	t.Parallel()
	down := domain.Directory{AgentByUID: lookup(nil, errDown), HumanByUID: lookup(nil, errDown), HumanByName: human(nil, errDown)}
	noNames := domain.Directory{AgentByUID: known.AgentByUID, HumanByUID: known.HumanByUID}
	cases := []struct {
		name   string
		peer   domain.Peer
		cgroup string
		dir    domain.Directory
		reason string
		cause  error
	}{
		{
			"uid 0 outside every agent cgroup", domain.Peer{PID: 16, UID: 0}, "0::/", known,
			domain.ReasonRootOutsideAgents, nil,
		},
		{
			"uid 0 in the daemon's cgroup is not the daemon (its terminals live there)", domain.Peer{PID: 17, UID: 0}, "0::/daemon", known,
			domain.ReasonRootOutsideAgents, nil,
		},
		{
			"uid in the agent range that no agent owns", domain.Peer{PID: 18, UID: 200002}, "0::/", known,
			domain.ReasonNoAgent, core.ErrNotFound,
		},
		{
			"uid of no agent and no human", domain.Peer{PID: 19, UID: 4242}, "0::/", known,
			domain.ReasonNobody, core.ErrNotFound,
		},
		{
			"a service user such as nobody", domain.Peer{PID: 20, UID: 65534}, "0::/daemon", known,
			domain.ReasonNobody, core.ErrNotFound,
		},
		{
			"agent directory lookup fails", domain.Peer{PID: 21, UID: 200001}, "0::/", down,
			domain.ReasonAgentLookup, errDown,
		},
		{
			"human directory lookup fails", domain.Peer{PID: 22, UID: 1000}, "0::/", down,
			domain.ReasonHumanLookup, errDown,
		},
		{
			"human cgroup of no human", domain.Peer{PID: 26, UID: 0}, "0::/humans/ghost", known,
			domain.ReasonNoHuman, core.ErrNotFound,
		},
		{
			"human cgroup with a name no human can have", domain.Peer{PID: 27, UID: 0}, "0::/humans/Ada", known,
			domain.ReasonNoHuman, nil,
		},
		{
			"the humans cgroup itself", domain.Peer{PID: 28, UID: 0}, "0::/humans", known,
			domain.ReasonNoHuman, nil,
		},
		{
			"human lookup for a human cgroup fails", domain.Peer{PID: 29, UID: 0}, "0::/humans/ada", down,
			domain.ReasonHumanCgroupLookup, errDown,
		},
		{
			"no human lookup wired for a human cgroup", domain.Peer{PID: 30, UID: 0}, "0::/humans/ada", noNames,
			domain.ReasonHumanCgroupLookup, nil,
		},
		{
			"a non-root process in an unknown human cgroup", domain.Peer{PID: 31, UID: 1000}, "0::/humans/ghost", known,
			domain.ReasonNoHuman, core.ErrNotFound,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got, err := domain.ResolvePeer(context.Background(), c.peer, c.cgroup, daemonPID, c.dir)
			if !cmp.Equal(got, core.Anonymous) {
				t.Errorf("caller %+v, want Anonymous", got)
			}
			var r *domain.Refusal
			if !errors.As(err, &r) || !errors.Is(err, core.ErrForbidden) {
				t.Fatalf("err %v, want a Refusal matching ErrForbidden", err)
			}
			if r.Peer != c.peer || r.Reason != c.reason {
				t.Errorf("refusal %+v, want peer %+v reason %q", r, c.peer, c.reason)
			}
			if c.cause != nil && !errors.Is(err, c.cause) {
				t.Errorf("err %v does not carry %v", err, c.cause)
			}
		})
	}
}

func TestRefusal_SaysWhoAndWhy(t *testing.T) {
	t.Parallel()
	err := &domain.Refusal{Peer: domain.Peer{PID: 9, UID: 4242}, Reason: domain.ReasonNobody}
	if got, want := err.Error(), "socket peer pid 9 uid 4242 refused: "+domain.ReasonNobody; got != want {
		t.Fatalf("Error() = %q, want %q", got, want)
	}
	withCause := &domain.Refusal{Peer: domain.Peer{PID: 9, UID: 1000}, Reason: domain.ReasonHumanLookup, Err: errDown}
	if got, want := withCause.Error(), "socket peer pid 9 uid 1000 refused: "+domain.ReasonHumanLookup+": "+errDown.Error(); got != want {
		t.Fatalf("Error() = %q, want %q", got, want)
	}
}
