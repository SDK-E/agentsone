// SPDX-License-Identifier: MIT

package system

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/SDK-E/agentsone/internal/machine/app"
	"github.com/SDK-E/agentsone/internal/machine/domain"
)

// MetricsPaths says where the metrics come from. In the container the
// cgroup's cpu.stat covers the whole machine service.
type MetricsPaths struct {
	Proc    string   // /proc
	CPUStat string   // /sys/fs/cgroup/cpu.stat
	Mounts  []string // file systems to report
}

// Metrics reads /proc and the container's cgroup.
type Metrics struct {
	paths MetricsPaths
	now   func() time.Time

	mu   sync.Mutex
	last domain.CPUSample
}

var _ app.MetricsReader = (*Metrics)(nil)

// NewMetrics reads metrics from paths; now is the clock for CPU samples.
func NewMetrics(paths MetricsPaths, now func() time.Time) *Metrics {
	return &Metrics{paths: paths, now: now}
}

// Read takes a snapshot. CPU use is measured since the previous Read (or
// since the cgroup started, on the first one).
func (m *Metrics) Read(context.Context) (domain.Metrics, error) {
	out := domain.Metrics{Cores: runtime.NumCPU()}
	out.Hostname, _ = os.Hostname()
	out.Uptime = time.Duration(Procs{root: m.paths.Proc}.uptime() * float64(time.Second))
	out.Load = m.load()
	out.MemTotal, out.MemAvail = m.memory()
	out.MemUsed = out.MemTotal - out.MemAvail
	out.RxBytes, out.TxBytes = m.network()
	out.CPUPercent = m.cpu(out.Cores, out.Uptime)
	for _, mount := range m.paths.Mounts {
		var st syscall.Statfs_t
		if syscall.Statfs(mount, &st) == nil {
			bs := int64(st.Bsize)               //nolint:unconvert // Bsize is uint32 on darwin, int64 on linux
			total := int64(st.Blocks)           //nolint:gosec // G115: a real file system's block count fits int64.
			used := int64(st.Blocks - st.Bfree) //nolint:gosec // G115: as above.
			out.Disks = append(out.Disks, domain.Disk{Mount: mount, Total: total * bs, Used: used * bs})
		}
	}
	return out, nil
}

func (m *Metrics) load() [3]float64 {
	b, _ := os.ReadFile(filepath.Join(m.paths.Proc, "loadavg"))
	f := strings.Fields(string(b))
	var l [3]float64
	for i := 0; i < 3 && i < len(f); i++ {
		l[i], _ = strconv.ParseFloat(f[i], 64)
	}
	return l
}

func (m *Metrics) memory() (total, avail int64) {
	for _, line := range readLines(filepath.Join(m.paths.Proc, "meminfo")) {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		kb, _ := strconv.ParseInt(f[1], 10, 64)
		switch f[0] {
		case "MemTotal:":
			total = kb * 1024
		case "MemAvailable:":
			avail = kb * 1024
		}
	}
	return total, avail
}

func (m *Metrics) network() (rx, tx int64) {
	for _, line := range readLines(filepath.Join(m.paths.Proc, "net/dev")) {
		name, rest, ok := strings.Cut(line, ":")
		if !ok || strings.TrimSpace(name) == "lo" {
			continue
		}
		f := strings.Fields(rest)
		if len(f) < 9 {
			continue
		}
		r, _ := strconv.ParseInt(f[0], 10, 64)
		t, _ := strconv.ParseInt(f[8], 10, 64)
		rx, tx = rx+r, tx+t
	}
	return rx, tx
}

func (m *Metrics) cpu(cores int, uptime time.Duration) float64 {
	var busy int64
	for _, line := range readLines(m.paths.CPUStat) {
		if v, ok := strings.CutPrefix(line, "usage_usec "); ok {
			busy, _ = strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		}
	}
	cur := domain.CPUSample{BusyMicros: busy, At: m.now()}
	m.mu.Lock()
	defer m.mu.Unlock()
	prev := m.last
	if prev.At.IsZero() {
		prev = domain.CPUSample{At: cur.At.Add(-uptime)}
	}
	m.last = cur
	return domain.CPUPercent(prev, cur, cores)
}
