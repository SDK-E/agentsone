// SPDX-License-Identifier: MIT

// Package netwall reads the machine's network wall from /proc: the IPv4
// and IPv6 routing tables and the list of network interfaces of the
// daemon's own network namespace, which is the machine container's. It
// only reads; it never opens a socket.
package netwall

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"math/bits"
	"net/netip"
	"os"
	"strconv"
	"strings"

	supapp "github.com/SDK-E/agentsone/internal/agents/supervisor/app"
	"github.com/SDK-E/agentsone/internal/agents/supervisor/domain"
)

// Route flags from linux/route.h, the same for IPv4 and IPv6 rows.
const (
	rtfGateway = 0x0002
	rtfReject  = 0x0200
)

// Reader checks the wall from a /proc file system.
type Reader struct {
	// Proc is /proc; tests pass a map of sample files.
	Proc fs.FS
}

var _ supapp.NetworkWall = (*Reader)(nil)

// System reads the real /proc.
func System() *Reader { return &Reader{Proc: os.DirFS("/proc")} }

// Check reads the routing tables and the interfaces. Anything it cannot
// read or parse is no proof, so the wall then does not hold.
func (r *Reader) Check(context.Context) domain.NetWall {
	ifaces, err := interfaces(r.Proc)
	if err != nil {
		return domain.NetWallUnproven(err)
	}
	v4, err := routes4(r.Proc)
	if err != nil {
		return domain.NetWallUnproven(err)
	}
	v6, err := routes6(r.Proc)
	if err != nil {
		return domain.NetWallUnproven(err)
	}
	return domain.JudgeNetWall(ifaces, append(v4, v6...))
}

// interfaces lists the names in net/dev, after its two header lines.
func interfaces(proc fs.FS) ([]string, error) {
	lines, err := readLines(proc, "net/dev")
	if err != nil {
		return nil, err
	}
	if len(lines) < 2 {
		return nil, errors.New("net/dev: no header")
	}
	var out []string
	for _, l := range lines[2:] {
		name, _, ok := strings.Cut(l, ":")
		if !ok {
			return nil, fmt.Errorf("net/dev: line %q has no interface name", l)
		}
		out = append(out, strings.TrimSpace(name))
	}
	return out, nil
}

// routes4 reads net/route: Iface Destination Gateway Flags RefCnt Use
// Metric Mask MTU Window IRTT, addresses in hex as the kernel holds them.
func routes4(proc fs.FS) ([]domain.Route, error) {
	lines, err := readLines(proc, "net/route")
	if err != nil {
		return nil, err
	}
	var out []domain.Route
	for _, l := range lines[min(1, len(lines)):] {
		f := strings.Fields(l)
		if len(f) < 8 {
			return nil, fmt.Errorf("net/route: short line %q", l)
		}
		iface := f[0]
		flags, err := strconv.ParseUint(f[3], 16, 32)
		if err != nil {
			return nil, fmt.Errorf("net/route: flags of %q: %w", l, err)
		}
		dst, err1 := addr4(f[1])
		gw, err2 := addr4(f[2])
		mask, err3 := addr4(f[7])
		if err := errors.Join(err1, err2, err3); err != nil {
			return nil, fmt.Errorf("net/route: %q: %w", l, err)
		}
		if iface == "lo" || flags&rtfReject != 0 {
			continue
		}
		ones := bits.OnesCount32(binary.BigEndian.Uint32(mask.AsSlice()))
		gateway := flags&rtfGateway != 0 || !gw.IsUnspecified()
		out = append(out, domain.Route{
			Iface: iface,
			Out:   dst.IsUnspecified() || gateway,
			Desc:  describe(netip.PrefixFrom(dst, ones), gw, gateway, iface),
		})
	}
	return out, nil
}

// routes6 reads net/ipv6_route: destination, its prefix length, source,
// its prefix length, next hop, metric, use count, reference count, flags,
// device. A kernel without IPv6 has no such file and no IPv6 route.
func routes6(proc fs.FS) ([]domain.Route, error) {
	lines, err := readLines(proc, "net/ipv6_route")
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []domain.Route
	for _, l := range lines {
		f := strings.Fields(l)
		if len(f) < 10 {
			return nil, fmt.Errorf("net/ipv6_route: short line %q", l)
		}
		iface := f[9]
		flags, err := strconv.ParseUint(f[8], 16, 32)
		if err != nil {
			return nil, fmt.Errorf("net/ipv6_route: flags of %q: %w", l, err)
		}
		plen, err := strconv.ParseUint(f[1], 16, 8)
		if err != nil {
			return nil, fmt.Errorf("net/ipv6_route: prefix of %q: %w", l, err)
		}
		dst, err1 := addr6(f[0])
		hop, err2 := addr6(f[4])
		if err := errors.Join(err1, err2); err != nil {
			return nil, fmt.Errorf("net/ipv6_route: %q: %w", l, err)
		}
		if iface == "lo" || flags&rtfReject != 0 {
			continue
		}
		gateway := flags&rtfGateway != 0 || !hop.IsUnspecified()
		out = append(out, domain.Route{
			Iface: iface,
			Out:   (dst.IsUnspecified() && plen == 0) || gateway,
			Desc:  describe(netip.PrefixFrom(dst, int(plen)), hop, gateway, iface),
		})
	}
	return out, nil
}

// describe names a route the way ip route prints it.
func describe(dst netip.Prefix, gw netip.Addr, gateway bool, iface string) string {
	if gateway {
		return dst.String() + " via " + gw.String() + " dev " + iface
	}
	return dst.String() + " dev " + iface
}

// addr4 decodes an IPv4 address that the kernel printed from memory as one
// hex number, in the host's byte order.
func addr4(s string) (netip.Addr, error) {
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil || len(s) != 8 {
		return netip.Addr{}, fmt.Errorf("IPv4 address %q is not 8 hex digits", s)
	}
	var b [4]byte
	binary.NativeEndian.PutUint32(b[:], uint32(v))
	return netip.AddrFrom4(b), nil
}

// addr6 decodes an IPv6 address printed as 32 hex digits.
func addr6(s string) (netip.Addr, error) {
	b, err := hex.DecodeString(s)
	if err != nil || len(b) != 16 {
		return netip.Addr{}, fmt.Errorf("IPv6 address %q is not 32 hex digits", s)
	}
	return netip.AddrFrom16([16]byte(b)), nil
}

func readLines(proc fs.FS, name string) ([]string, error) {
	b, err := fs.ReadFile(proc, name)
	if err != nil {
		return nil, err
	}
	var out []string
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		if l := strings.TrimSpace(sc.Text()); l != "" {
			out = append(out, l)
		}
	}
	return out, sc.Err()
}
