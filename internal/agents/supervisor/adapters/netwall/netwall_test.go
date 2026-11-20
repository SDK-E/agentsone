// SPDX-License-Identifier: MIT

package netwall_test

import (
	"context"
	"errors"
	"io/fs"
	"testing"
	"testing/fstest"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/SDK-E/agentsone/internal/agents/supervisor/adapters/netwall"
	"github.com/SDK-E/agentsone/internal/agents/supervisor/domain"
)

// The samples below are /proc/net files as the kernel writes them, padding
// and all, from a machine container on an internal network (the compose
// stack), on Docker's default bridge, and with a second network attached
// the way Coolify's normal mode does.

const routeHeader = "Iface\tDestination\tGateway \tFlags\tRefCnt\tUse\tMetric\tMask\t\tMTU\tWindow\tIRTT                                                       \n"

// 10.77.42.0/27 on eth0, no gateway: the inner network.
const routeInner = routeHeader +
	"eth0\t002A4D0A\t00000000\t0001\t0\t0\t0\tE0FFFFFF\t0\t0\t0                                                                               \n"

// Docker's default bridge: default route through 172.17.0.1.
const routeBridge = routeHeader +
	"eth0\t00000000\t010011AC\t0003\t0\t0\t0\t00000000\t0\t0\t0                                                                               \n" +
	"eth0\t000011AC\t00000000\t0001\t0\t0\t0\t0000FFFF\t0\t0\t0                                                                               \n"

// Inner network plus a second one with the default route (Coolify's
// normal mode).
const routeTwoNets = routeHeader +
	"eth1\t00000000\t0101000A\t0003\t0\t0\t0\t00000000\t0\t0\t0                                                                               \n" +
	"eth0\t002A4D0A\t00000000\t0001\t0\t0\t0\tE0FFFFFF\t0\t0\t0                                                                               \n" +
	"eth1\t0001000A\t00000000\t0001\t0\t0\t0\t00FFFFFF\t0\t0\t0                                                                               \n"

// IPv6 on with no IPv6 network: link-local and multicast on eth0, and the
// kernel's own unreachable default route on lo, which leads nowhere.
const ipv6Quiet = "" +
	"fe800000000000000000000000000000 40 00000000000000000000000000000000 00 00000000000000000000000000000000 00000100 00000001 00000000 00000001     eth0\n" +
	"00000000000000000000000000000001 80 00000000000000000000000000000000 00 00000000000000000000000000000000 00000000 00000002 00000000 80200001       lo\n" +
	"fe80000000000000a8c1abfffe2a4d10 80 00000000000000000000000000000000 00 00000000000000000000000000000000 00000000 00000002 00000000 80200001     eth0\n" +
	"ff000000000000000000000000000000 08 00000000000000000000000000000000 00 00000000000000000000000000000000 00000100 00000003 00000000 00000001     eth0\n" +
	"00000000000000000000000000000000 00 00000000000000000000000000000000 00 00000000000000000000000000000000 ffffffff 00000001 00000000 00200200       lo\n"

// An IPv6 network with a default route through fd00::1.
const ipv6Default = ipv6Quiet +
	"00000000000000000000000000000000 00 00000000000000000000000000000000 00 fd000000000000000000000000000001 00000400 00000001 00000000 00000003     eth0\n"

const devHeader = "Inter-|   Receive                                                |  Transmit\n" +
	" face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed\n"

const devInner = devHeader +
	"    lo:       0       0    0    0    0     0          0         0        0       0    0    0    0     0       0          0\n" +
	"  eth0:    1296      16    0    0    0     0          0         0      656       8    0    0    0     0       0          0\n"

const devTwoNets = devInner +
	"  eth1:     900      10    0    0    0     0          0         0      300       4    0    0    0     0       0          0\n"

// A host with the ipip and sit modules loaded gives every namespace these
// two, down and unrouted.
const devTunnels = devInner +
	" tunl0:       0       0    0    0    0     0          0         0        0       0    0    0    0     0       0          0\n" +
	"  sit0:       0       0    0    0    0     0          0         0        0       0    0    0    0     0       0          0\n"

func proc(route, ipv6, dev string) fstest.MapFS {
	m := fstest.MapFS{}
	if route != "" {
		m["net/route"] = &fstest.MapFile{Data: []byte(route)}
	}
	if ipv6 != "" {
		m["net/ipv6_route"] = &fstest.MapFile{Data: []byte(ipv6)}
	}
	if dev != "" {
		m["net/dev"] = &fstest.MapFile{Data: []byte(dev)}
	}
	return m
}

func TestReader_Check(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		proc fstest.MapFS
		want domain.NetWall
	}{
		{
			name: "compose stack: inner network only, IPv6 quiet",
			proc: proc(routeInner, ipv6Quiet, devInner),
			want: domain.NetWall{Holds: true, Interfaces: []string{"eth0"}},
		},
		{
			name: "kernel without IPv6 has no ipv6_route",
			proc: proc(routeInner, "", devInner),
			want: domain.NetWall{Holds: true, Interfaces: []string{"eth0"}},
		},
		{
			name: "fallback tunnels of the host's modules are no network",
			proc: proc(routeInner, ipv6Quiet, devTunnels),
			want: domain.NetWall{Holds: true, Interfaces: []string{"eth0"}},
		},
		{
			name: "no network: loopback only",
			proc: proc(routeHeader, "", devHeader+"    lo:       0       0    0    0    0     0          0         0        0       0    0    0    0     0       0          0\n"),
			want: domain.NetWall{Holds: true},
		},
		{
			name: "default bridge: IPv4 default route",
			proc: proc(routeBridge, ipv6Quiet, devInner),
			want: domain.NetWall{
				Interfaces: []string{"eth0"},
				RoutesOut:  []string{"0.0.0.0/0 via 172.17.0.1 dev eth0"},
				Reason:     "the machine has a direct route to the internet (through eth0)",
				Fix:        domain.NetWallFix,
			},
		},
		{
			name: "IPv6 default route",
			proc: proc(routeInner, ipv6Default, devInner),
			want: domain.NetWall{
				Interfaces: []string{"eth0"},
				RoutesOut:  []string{"::/0 via fd00::1 dev eth0"},
				Reason:     "the machine has a direct route to the internet (through eth0)",
				Fix:        domain.NetWallFix,
			},
		},
		{
			name: "Coolify normal mode: second network with the default route",
			proc: proc(routeTwoNets, ipv6Quiet, devTwoNets),
			want: domain.NetWall{
				Interfaces: []string{"eth0", "eth1"},
				RoutesOut:  []string{"0.0.0.0/0 via 10.0.1.1 dev eth1"},
				Reason:     "the machine has a direct route to the internet (through eth1)",
				Fix:        domain.NetWallFix,
			},
		},
		{
			name: "second network without a route out",
			proc: proc(routeInner, ipv6Quiet, devTwoNets),
			want: domain.NetWall{
				Interfaces: []string{"eth0", "eth1"},
				Reason:     "the machine is on more than one network (eth0, eth1)",
				Fix:        domain.NetWallFix,
			},
		},
		{
			name: "a gateway route that is not the default still leads out",
			proc: proc(routeInner+"eth0\t00010101\t012A4D0A\t0003\t0\t0\t0\t00FFFFFF\t0\t0\t0\n", ipv6Quiet, devInner),
			want: domain.NetWall{
				Interfaces: []string{"eth0"},
				RoutesOut:  []string{"1.1.1.0/24 via 10.77.42.1 dev eth0"},
				Reason:     "the machine has a direct route to the internet (through eth0)",
				Fix:        domain.NetWallFix,
			},
		},
		{
			name: "half the internet without a gateway is a route out",
			proc: proc(routeInner+"eth0\t00000000\t00000000\t0001\t0\t0\t0\t00000080\t0\t0\t0\n", ipv6Quiet, devInner),
			want: domain.NetWall{
				Interfaces: []string{"eth0"},
				RoutesOut:  []string{"0.0.0.0/1 dev eth0"},
				Reason:     "the machine has a direct route to the internet (through eth0)",
				Fix:        domain.NetWallFix,
			},
		},
		{
			name: "an unreachable default route leads nowhere",
			proc: proc(routeInner+"*\t00000000\t00000000\t0201\t0\t0\t0\t00000000\t0\t0\t0\n", ipv6Quiet, devInner),
			want: domain.NetWall{Holds: true, Interfaces: []string{"eth0"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := (&netwall.Reader{Proc: tt.proc}).Check(context.Background())
			if diff := cmp.Diff(tt.want, got, cmpopts.EquateEmpty(), cmpopts.EquateErrors()); diff != "" {
				t.Errorf("Check() (-want +got):\n%s", diff)
			}
		})
	}
}

// What cannot be read or parsed is no proof: the wall then does not hold.
func TestReader_CheckFailsClosed(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		proc fstest.MapFS
	}{
		{"no routing table (not Linux)", fstest.MapFS{}},
		{"no IPv4 routing table", proc("", ipv6Quiet, devInner)},
		{"no interface list", proc(routeInner, ipv6Quiet, "")},
		{"IPv4 row too short", proc(routeHeader+"eth0\t00000000\t00000000\n", ipv6Quiet, devInner)},
		{"IPv4 flags not hex", proc(routeHeader+"eth0\t00000000\t00000000\tzz\t0\t0\t0\t00000000\t0\t0\t0\n", ipv6Quiet, devInner)},
		{"IPv4 address not hex", proc(routeHeader+"eth0\t0000XX00\t00000000\t0001\t0\t0\t0\t00000000\t0\t0\t0\n", ipv6Quiet, devInner)},
		{"IPv4 mask not hex", proc(routeHeader+"eth0\t00000000\t00000000\t0001\t0\t0\t0\tXX\t0\t0\t0\n", ipv6Quiet, devInner)},
		{"IPv4 gateway not hex", proc(routeHeader+"eth0\t00000000\tnothex!!\t0001\t0\t0\t0\t00000000\t0\t0\t0\n", ipv6Quiet, devInner)},
		{"IPv6 row too short", proc(routeInner, "00000000000000000000000000000000 00\n", devInner)},
		{"IPv6 flags not hex", proc(routeInner, "00000000000000000000000000000000 00 00000000000000000000000000000000 00 00000000000000000000000000000000 00000400 00000001 00000000 zzzzzzzz eth0\n", devInner)},
		{"IPv6 address not hex", proc(routeInner, "0000000000000000000000000000zz00 00 00000000000000000000000000000000 00 00000000000000000000000000000000 00000400 00000001 00000000 00000001 eth0\n", devInner)},
		{"IPv6 prefix not hex", proc(routeInner, "00000000000000000000000000000000 zz 00000000000000000000000000000000 00 00000000000000000000000000000000 00000400 00000001 00000000 00000001 eth0\n", devInner)},
		{"IPv6 next hop not hex", proc(routeInner, "00000000000000000000000000000000 00 00000000000000000000000000000000 00 0000000000000000000000000000zz00 00000400 00000001 00000000 00000001 eth0\n", devInner)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := (&netwall.Reader{Proc: tt.proc}).Check(context.Background())
			if got.Holds || got.Err == nil {
				t.Fatalf("Check() = %+v, want a wall that does not hold, with the cause", got)
			}
			if got.Reason != domain.NetWallUnproven(nil).Reason {
				t.Errorf("Reason = %q", got.Reason)
			}
		})
	}
}

func TestReader_CheckReadsTheRealFileSystemByDefault(t *testing.T) {
	t.Parallel()
	got := netwall.System().Check(context.Background())
	if got.Err != nil && !errors.Is(got.Err, fs.ErrNotExist) {
		t.Fatalf("Check() on this host: %v", got.Err)
	}
}
