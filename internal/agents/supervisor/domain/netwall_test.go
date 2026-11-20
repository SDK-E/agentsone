// SPDX-License-Identifier: MIT

package domain_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/SDK-E/agentsone/internal/agents/supervisor/domain"
)

func TestJudgeNetWall(t *testing.T) {
	t.Parallel()
	inner := domain.Route{Iface: "eth0", Desc: "10.77.42.0/27 dev eth0"}
	out := domain.Route{Iface: "eth1", Out: true, Desc: "0.0.0.0/0 via 10.0.1.1 dev eth1"}
	tests := []struct {
		name   string
		ifaces []string
		routes []domain.Route
		want   domain.NetWall
	}{
		{
			name:   "inner network only",
			ifaces: []string{"lo", "eth0"},
			routes: []domain.Route{inner},
			want:   domain.NetWall{Holds: true, Interfaces: []string{"eth0"}},
		},
		{
			name:   "no network at all",
			ifaces: []string{"lo"},
			want:   domain.NetWall{Holds: true},
		},
		{
			name:   "kernel fallback tunnels without routes are not networks",
			ifaces: []string{"lo", "eth0", "tunl0", "sit0", "ip6tnl0", "gre0", "gretap0", "erspan0", "ip6gre0", "ip_vti0", "ip6_vti0"},
			routes: []domain.Route{inner},
			want:   domain.NetWall{Holds: true, Interfaces: []string{"eth0"}},
		},
		{
			name:   "a fallback tunnel that carries a route is a network",
			ifaces: []string{"lo", "eth0", "sit0"},
			routes: []domain.Route{inner, {Iface: "sit0", Desc: "2001:db8::/64 dev sit0"}},
			want: domain.NetWall{
				Interfaces: []string{"eth0", "sit0"},
				Reason:     "the machine is on more than one network (eth0, sit0)",
				Fix:        domain.NetWallFix,
			},
		},
		{
			name:   "a second network",
			ifaces: []string{"lo", "eth1", "eth0"},
			routes: []domain.Route{inner, {Iface: "eth1", Desc: "10.0.1.0/24 dev eth1"}},
			want: domain.NetWall{
				Interfaces: []string{"eth0", "eth1"},
				Reason:     "the machine is on more than one network (eth0, eth1)",
				Fix:        domain.NetWallFix,
			},
		},
		{
			name:   "a route out wins over a second network",
			ifaces: []string{"lo", "eth0", "eth1"},
			routes: []domain.Route{inner, out},
			want: domain.NetWall{
				Interfaces: []string{"eth0", "eth1"},
				RoutesOut:  []string{"0.0.0.0/0 via 10.0.1.1 dev eth1"},
				Reason:     "the machine has a direct route to the internet (through eth1)",
				Fix:        domain.NetWallFix,
			},
		},
		{
			name:   "a route out on the only network",
			ifaces: []string{"lo", "eth0"},
			routes: []domain.Route{inner, {Iface: "eth0", Out: true, Desc: "0.0.0.0/0 via 172.17.0.1 dev eth0"}, {Iface: "eth0", Out: true, Desc: "::/0 via fd00::1 dev eth0"}},
			want: domain.NetWall{
				Interfaces: []string{"eth0"},
				RoutesOut:  []string{"0.0.0.0/0 via 172.17.0.1 dev eth0", "::/0 via fd00::1 dev eth0"},
				Reason:     "the machine has a direct route to the internet (through eth0)",
				Fix:        domain.NetWallFix,
			},
		},
		{
			name:   "an interface that is not listed still counts by its route",
			ifaces: []string{"lo"},
			routes: []domain.Route{inner},
			want:   domain.NetWall{Holds: true, Interfaces: []string{"eth0"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := domain.JudgeNetWall(tt.ifaces, tt.routes)
			if diff := cmp.Diff(tt.want, got, cmpopts.EquateEmpty(), cmpopts.EquateErrors()); diff != "" {
				t.Errorf("JudgeNetWall() (-want +got):\n%s", diff)
			}
		})
	}
}

func TestNetWallUnproven_FailsClosedAndKeepsTheCause(t *testing.T) {
	t.Parallel()
	cause := errors.New("open net/route: file does not exist")
	w := domain.NetWallUnproven(cause)
	if w.Holds {
		t.Fatal("an unreadable routing table must not count as a wall that holds")
	}
	if !errors.Is(w.Err, cause) {
		t.Errorf("Err = %v, want the cause", w.Err)
	}
	if w.Reason != "the machine's routing table cannot be read, so the wall cannot be proven" {
		t.Errorf("Reason = %q", w.Reason)
	}
	if strings.Contains(w.StateReason(), "does not exist") {
		t.Errorf("StateReason() = %q, must not carry the error's detail", w.StateReason())
	}
}

func TestNetWall_StateReasonIsOnePlainSentenceWithTheFix(t *testing.T) {
	t.Parallel()
	w := domain.JudgeNetWall([]string{"lo", "eth0"}, []domain.Route{{Iface: "eth0", Out: true, Desc: "0.0.0.0/0 via 172.17.0.1 dev eth0"}})
	want := "The network wall does not hold, so no agent may wake: the machine has a direct route to the internet (through eth0). " +
		"Deploy with Raw compose mode and do not attach other networks to the machine service."
	if got := w.StateReason(); got != want {
		t.Errorf("StateReason() =\n%q\nwant\n%q", got, want)
	}
	if n := len([]rune(w.StateReason())); n > 500 {
		t.Errorf("StateReason() has %d characters; the contract allows 500", n)
	}
	if got := (domain.NetWall{Holds: true}).StateReason(); got != "" {
		t.Errorf("StateReason() of a wall that holds = %q, want empty", got)
	}
}
