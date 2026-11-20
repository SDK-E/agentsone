// SPDX-License-Identifier: MIT

package load_test

import (
	"testing"
	"testing/fstest"

	"github.com/SDK-E/agentsone/emitters/load"
)

const procStat = `cpu  100 20 30 800 40 5 5 0 0 0
cpu0 50 10 15 400 20 2 3 0 0 0
intr 1 2 3
`

const procMeminfo = `MemTotal:       16384000 kB
MemFree:         1000000 kB
MemAvailable:    4096000 kB
Buffers:          100000 kB
`

func TestProc_ReadsCPUAndMemory(t *testing.T) {
	t.Parallel()
	p := load.Proc{FS: fstest.MapFS{
		"stat":    {Data: []byte(procStat)},
		"meminfo": {Data: []byte(procMeminfo)},
	}}

	cpu, err := p.CPU()
	if err != nil {
		t.Fatalf("CPU error = %v", err)
	}
	// user+nice+system+irq+softirq+steal busy; idle+iowait idle.
	if cpu.Total != 1000 || cpu.Busy != 160 {
		t.Fatalf("cpu = %+v, want busy 160 of total 1000", cpu)
	}
	mem, err := p.Memory()
	if err != nil {
		t.Fatalf("Memory error = %v", err)
	}
	if mem.TotalBytes != 16384000*1024 || mem.AvailableBytes != 4096000*1024 {
		t.Fatalf("mem = %+v", mem)
	}
}

func TestProc_BadFilesAreErrors(t *testing.T) {
	t.Parallel()
	for name, fsys := range map[string]fstest.MapFS{
		"missing files":      {},
		"stat without cpu":   {"stat": {Data: []byte("intr 1\n")}, "meminfo": {Data: []byte(procMeminfo)}},
		"short cpu line":     {"stat": {Data: []byte("cpu 1 2\n")}, "meminfo": {Data: []byte(procMeminfo)}},
		"non numeric cpu":    {"stat": {Data: []byte("cpu a b c d e f g\n")}, "meminfo": {Data: []byte(procMeminfo)}},
		"meminfo no total":   {"stat": {Data: []byte(procStat)}, "meminfo": {Data: []byte("MemAvailable: 1 kB\n")}},
		"meminfo no avail":   {"stat": {Data: []byte(procStat)}, "meminfo": {Data: []byte("MemTotal: 1 kB\n")}},
		"meminfo non number": {"stat": {Data: []byte(procStat)}, "meminfo": {Data: []byte("MemTotal: x kB\nMemAvailable: 1 kB\n")}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			p := load.Proc{FS: fsys}
			_, cpuErr := p.CPU()
			_, memErr := p.Memory()
			if cpuErr == nil && memErr == nil {
				t.Fatal("both reads succeeded, want at least one error")
			}
		})
	}
}
