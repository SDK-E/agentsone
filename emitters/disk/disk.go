// SPDX-License-Identifier: MIT

// Package disk is the built-in disk event source. It samples every real
// mounted file system and publishes agentsone.disk.usage.high when one
// reaches its threshold and agentsone.disk.usage.normal when it has fallen
// back clear of it. It publishes on the change, not on every sample, and a
// small margin below the threshold keeps a file system that hovers at the
// line from flapping. A file system that is already full when the source
// starts is reported high once; a restart while full reports it again, so
// listeners must treat the event as at-least-once.
package disk

import (
	"context"
	"time"

	"github.com/SDK-E/agentsone/internal/emitkit/app"
	"github.com/SDK-E/agentsone/internal/emitkit/domain"
)

// Config is how the source behaves.
type Config struct {
	// ThresholdPercent is the used share at which a file system is high.
	ThresholdPercent int
	// MarginPercent is how far below the threshold it must fall to be normal.
	MarginPercent int
	// Interval is the time between samples.
	Interval time.Duration
}

// DefaultConfig is what the machine runs with.
func DefaultConfig() Config {
	return Config{ThresholdPercent: 90, MarginPercent: 2, Interval: time.Minute}
}

// DefaultMountsFile is the kernel's mount table.
const DefaultMountsFile = "/proc/self/mounts"

// Source is the disk source.
type Source struct {
	cfg    Config
	mounts MountLister
	stat   Statter
	gates  map[string]*domain.Gate
}

var _ app.Source = (*Source)(nil)

// New returns a source over the given mount table and statter.
func New(cfg Config, mounts MountLister, stat Statter) *Source {
	return &Source{cfg: cfg, mounts: mounts, stat: stat, gates: map[string]*domain.Gate{}}
}

// Run samples until ctx is cancelled.
func (s *Source) Run(ctx context.Context, d app.Deps) error {
	return app.Every(ctx, d.Sleeper, s.cfg.Interval, d.Log, func(ctx context.Context) error {
		return s.sample(ctx, d)
	})
}

func (s *Source) sample(ctx context.Context, d app.Deps) error {
	mounts, err := s.mounts.List()
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, m := range mounts {
		if pseudo[m.FSType] || seen[m.Path] {
			continue
		}
		seen[m.Path] = true
		u, err := s.stat.Usage(m.Path)
		if err != nil {
			d.Log.WarnContext(ctx, "cannot read file system", "mount", m.Path, "error", err)
			continue
		}
		if u.TotalBytes == 0 {
			continue
		}
		s.observe(ctx, d, m.Path, u)
	}
	for path := range s.gates { // a file system that went away takes its state with it
		if !seen[path] {
			delete(s.gates, path)
		}
	}
	return nil
}

func (s *Source) observe(ctx context.Context, d app.Deps, path string, u Usage) {
	g, ok := s.gates[path]
	if !ok {
		g = domain.NewGate(s.cfg.ThresholdPercent, s.cfg.MarginPercent)
		s.gates[path] = g
	}
	pct := u.Percent()
	var draft domain.Draft
	var err error
	switch g.Observe(pct) {
	case domain.WentHigh:
		draft, err = domain.NewDraft("agentsone.disk.usage.high", path, map[string]any{
			"mount": path, "used_percent": pct, "threshold_percent": s.cfg.ThresholdPercent, "free_bytes": u.AvailBytes,
		})
	case domain.WentNormal:
		draft, err = domain.NewDraft("agentsone.disk.usage.normal", path, map[string]any{"mount": path, "used_percent": pct})
	default:
		return
	}
	if err != nil {
		d.Log.ErrorContext(ctx, "bad event draft", "mount", path, "error", err)
		return
	}
	if err := d.Publisher.Publish(ctx, draft); err != nil && ctx.Err() == nil {
		d.Log.WarnContext(ctx, "disk event not published", "type", draft.Type, "mount", path, "error", err)
	}
}
