// SPDX-License-Identifier: MIT

package load

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"strconv"
	"strings"
)

// Proc is the real Meter: it reads the kernel's /proc files from FS, which is
// os.DirFS("/proc") on a machine and a fake in tests.
type Proc struct{ FS fs.FS }

var _ Meter = Proc{}

// CPU reads the aggregate "cpu" line of stat. Busy is user, nice, system,
// irq, softirq and steal; idle and iowait count as idle. Guest time is
// already inside user.
func (p Proc) CPU() (CPUSample, error) {
	raw, err := fs.ReadFile(p.FS, "stat")
	if err != nil {
		return CPUSample{}, fmt.Errorf("read stat: %w", err)
	}
	sc := bufio.NewScanner(bytes.NewReader(raw))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 0 || f[0] != "cpu" {
			continue
		}
		if len(f) < 9 {
			return CPUSample{}, errors.New("stat: the cpu line has fewer than 8 counters")
		}
		var n [8]uint64
		for i := range n {
			v, err := strconv.ParseUint(f[i+1], 10, 64)
			if err != nil {
				return CPUSample{}, fmt.Errorf("stat: bad cpu counter %q: %w", f[i+1], err)
			}
			n[i] = v
		}
		// user nice system idle iowait irq softirq steal
		busy := n[0] + n[1] + n[2] + n[5] + n[6] + n[7]
		return CPUSample{Busy: busy, Total: busy + n[3] + n[4]}, nil
	}
	return CPUSample{}, errors.New("stat: no cpu line")
}

// Memory reads MemTotal and MemAvailable from meminfo.
func (p Proc) Memory() (MemInfo, error) {
	raw, err := fs.ReadFile(p.FS, "meminfo")
	if err != nil {
		return MemInfo{}, fmt.Errorf("read meminfo: %w", err)
	}
	var total, avail uint64
	var haveTotal, haveAvail bool
	sc := bufio.NewScanner(bytes.NewReader(raw))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 2 {
			continue
		}
		var dst *uint64
		switch f[0] {
		case "MemTotal:":
			dst, haveTotal = &total, true
		case "MemAvailable:":
			dst, haveAvail = &avail, true
		default:
			continue
		}
		kb, err := strconv.ParseUint(f[1], 10, 64)
		if err != nil {
			return MemInfo{}, fmt.Errorf("meminfo: bad %s value %q: %w", f[0], f[1], err)
		}
		*dst = kb * 1024
	}
	if !haveTotal || !haveAvail {
		return MemInfo{}, errors.New("meminfo: MemTotal or MemAvailable is missing")
	}
	return MemInfo{TotalBytes: total, AvailableBytes: avail}, nil
}
