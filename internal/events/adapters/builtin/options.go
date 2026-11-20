// SPDX-License-Identifier: MIT

package builtin

import (
	"errors"
	"fmt"
	"time"

	"github.com/SDK-E/agentsone/internal/core"
)

// DiskOptions is the [emitters.disk] table.
type DiskOptions struct {
	ThresholdPercent int           `toml:"threshold_percent"`
	MarginPercent    int           `toml:"margin_percent"`
	Interval         core.Duration `toml:"interval"`
	MountsFile       string        `toml:"mounts_file"`
}

// Options is the [emitters] table of the daemon config. Every key has a
// default, so a machine with no table runs the sources as designed.
type Options struct {
	Disk DiskOptions `toml:"disk"`
}

// DefaultOptions are the settings a machine runs with.
func DefaultOptions() Options {
	return Options{Disk: DiskOptions{
		ThresholdPercent: 90,
		MarginPercent:    2,
		Interval:         core.Duration{Duration: time.Minute},
		MountsFile:       "/proc/self/mounts",
	}}
}

// LoadOptions reads [emitters] over the defaults. Unknown keys and values
// that cannot work are errors, so a typo stops the daemon instead of
// silently turning a source off.
func LoadOptions(cfg *core.Config) (Options, error) {
	o := DefaultOptions()
	if cfg == nil {
		return o, nil
	}
	if err := cfg.Section("emitters", &o); err != nil {
		return Options{}, err
	}
	if err := o.validate(); err != nil {
		return Options{}, fmt.Errorf("[emitters]: %w", err)
	}
	return o, nil
}

func (o Options) validate() error {
	d := o.Disk
	switch {
	case d.ThresholdPercent < 1 || d.ThresholdPercent > 100:
		return errors.New("disk.threshold_percent must be 1 to 100")
	case d.MarginPercent < 0 || d.MarginPercent >= d.ThresholdPercent:
		return errors.New("disk.margin_percent must be at least 0 and below the threshold")
	case d.Interval.Duration <= 0:
		return errors.New("disk.interval must be positive")
	case d.MountsFile == "":
		return errors.New("disk.mounts_file must not be empty")
	}
	return nil
}
