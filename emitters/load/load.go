// SPDX-License-Identifier: MIT

// Package load is the built-in load event source. It samples CPU and memory
// and publishes agentsone.load.cpu.high or agentsone.load.memory.high when
// one stays at or above its threshold for the whole window, and
// agentsone.load.normal when both are back under their thresholds (by a
// small margin, so a machine at the line does not flap). One event per
// change, never one per sample.
//
// CPU use is the difference between two samples of the kernel's counters, so
// the first CPU reading comes with the second sample. The CPU and memory
// numbers are the ones /proc shows, which inside a container are the host's.
// The "top" list of cpu.high is empty until the daemon tracks the units it
// supervises.
package load

import (
	"context"
	"time"

	"github.com/SDK-E/agentsone/internal/emitkit/app"
	"github.com/SDK-E/agentsone/internal/emitkit/domain"
)

// CPUSample is the CPU time counters at one moment.
type CPUSample struct{ Busy, Total uint64 }

// MemInfo is memory at one moment.
type MemInfo struct{ TotalBytes, AvailableBytes uint64 }

// Meter reads the machine.
type Meter interface {
	CPU() (CPUSample, error)
	Memory() (MemInfo, error)
}

// Config is how the source behaves.
type Config struct {
	CPUThresholdPercent    int
	MemoryThresholdPercent int
	// MarginPercent is how far below its threshold a reading must fall to be normal.
	MarginPercent int
	// Interval is the time between samples.
	Interval time.Duration
	// Window is how long a reading must stay high before it is reported.
	Window time.Duration
}

// DefaultConfig is what the machine runs with: 90 percent for a minute,
// sampled every 10 seconds.
func DefaultConfig() Config {
	return Config{
		CPUThresholdPercent: 90, MemoryThresholdPercent: 90, MarginPercent: 5,
		Interval: 10 * time.Second, Window: time.Minute,
	}
}

// Source is the load source.
type Source struct {
	cfg    Config
	meter  Meter
	cpu    *domain.Gate
	mem    *domain.Gate
	prev   *CPUSample
	lastCP int
	lastMP int
}

var _ app.Source = (*Source)(nil)

// New returns a source over m.
func New(cfg Config, m Meter) *Source {
	sustain := 1
	if cfg.Interval > 0 {
		sustain = int((cfg.Window + cfg.Interval - 1) / cfg.Interval)
	}
	return &Source{
		cfg:   cfg,
		meter: m,
		cpu:   domain.NewSustainedGate(cfg.CPUThresholdPercent, cfg.MarginPercent, sustain),
		mem:   domain.NewSustainedGate(cfg.MemoryThresholdPercent, cfg.MarginPercent, sustain),
	}
}

// Run samples until ctx is cancelled.
func (s *Source) Run(ctx context.Context, d app.Deps) error {
	return app.Every(ctx, d.Sleeper, s.cfg.Interval, d.Log, func(ctx context.Context) error {
		s.sample(ctx, d)
		return nil
	})
}

func (s *Source) sample(ctx context.Context, d app.Deps) {
	wasHigh := s.cpu.High() || s.mem.High()
	if pct, ok := s.cpuPercent(ctx, d); ok {
		s.lastCP = pct
		if s.cpu.Observe(pct) == domain.WentHigh {
			s.publish(ctx, d, "agentsone.load.cpu.high", map[string]any{
				"used_percent": pct, "threshold_percent": s.cfg.CPUThresholdPercent,
				"window_seconds": int(s.windowSeconds()), "top": []string{},
			})
		}
	}
	if pct, avail, ok := s.memPercent(ctx, d); ok {
		s.lastMP = pct
		if s.mem.Observe(pct) == domain.WentHigh {
			s.publish(ctx, d, "agentsone.load.memory.high", map[string]any{
				"used_percent": pct, "threshold_percent": s.cfg.MemoryThresholdPercent, "available_bytes": avail,
			})
		}
	}
	if wasHigh && !s.cpu.High() && !s.mem.High() {
		s.publish(ctx, d, "agentsone.load.normal", map[string]any{"cpu_percent": s.lastCP, "memory_percent": s.lastMP})
	}
}

func (s *Source) windowSeconds() time.Duration {
	if s.cfg.Window < time.Second {
		return 1
	}
	return s.cfg.Window / time.Second
}

// cpuPercent is the busy share since the previous sample. The first sample,
// a failed read and a sample with no time elapsed give no reading.
func (s *Source) cpuPercent(ctx context.Context, d app.Deps) (int, bool) {
	cur, err := s.meter.CPU()
	if err != nil {
		d.Log.WarnContext(ctx, "cannot read cpu", "error", err)
		return 0, false
	}
	prev := s.prev
	s.prev = &cur
	if prev == nil || cur.Total <= prev.Total || cur.Busy < prev.Busy {
		return 0, false
	}
	return int((cur.Busy - prev.Busy) * 100 / (cur.Total - prev.Total)), true // #nosec G115 -- busy is part of total, so at most 100
}

func (s *Source) memPercent(ctx context.Context, d app.Deps) (pct int, avail uint64, ok bool) {
	m, err := s.meter.Memory()
	if err != nil {
		d.Log.WarnContext(ctx, "cannot read memory", "error", err)
		return 0, 0, false
	}
	if m.TotalBytes == 0 || m.AvailableBytes > m.TotalBytes {
		return 0, 0, false
	}
	return int((m.TotalBytes - m.AvailableBytes) * 100 / m.TotalBytes), m.AvailableBytes, true // #nosec G115 -- at most 100
}

func (s *Source) publish(ctx context.Context, d app.Deps, eventType string, data map[string]any) {
	draft, err := domain.NewDraft(eventType, "", data)
	if err != nil {
		d.Log.ErrorContext(ctx, "bad event draft", "type", eventType, "error", err)
		return
	}
	if err := d.Publisher.Publish(ctx, draft); err != nil && ctx.Err() == nil {
		d.Log.WarnContext(ctx, "load event not published", "type", eventType, "error", err)
	}
}
