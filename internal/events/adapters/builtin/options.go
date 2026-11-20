// SPDX-License-Identifier: MIT

package builtin

import (
	"errors"
	"fmt"
	"path/filepath"
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

// LoadSourceOptions is the [emitters.load] table.
type LoadSourceOptions struct {
	CPUThresholdPercent    int           `toml:"cpu_threshold_percent"`
	MemoryThresholdPercent int           `toml:"memory_threshold_percent"`
	MarginPercent          int           `toml:"margin_percent"`
	Interval               core.Duration `toml:"interval"`
	Window                 core.Duration `toml:"window"`
	ProcDir                string        `toml:"proc_dir"`
}

// Options is the [emitters] table of the daemon config. Every key has a
// default, so a machine with no table runs the sources as designed.
type Options struct {
	Disk  DiskOptions        `toml:"disk"`
	Load  LoadSourceOptions  `toml:"load"`
	Files FilesSourceOptions `toml:"files"`
}

// FilesSourceOptions is the [emitters.files] table. With no watches the
// source does not run.
type FilesSourceOptions struct {
	Watches  []string      `toml:"watches"`
	Interval core.Duration `toml:"interval"`
	MaxFiles int           `toml:"max_files"`
}

// DefaultOptions are the settings a machine runs with.
func DefaultOptions() Options {
	return Options{
		Disk: DiskOptions{
			ThresholdPercent: 90,
			MarginPercent:    2,
			Interval:         core.Duration{Duration: time.Minute},
			MountsFile:       "/proc/self/mounts",
		},
		Load: LoadSourceOptions{
			CPUThresholdPercent:    90,
			MemoryThresholdPercent: 90,
			MarginPercent:          5,
			Interval:               core.Duration{Duration: 10 * time.Second},
			Window:                 core.Duration{Duration: time.Minute},
			ProcDir:                "/proc",
		},
		Files: FilesSourceOptions{
			Interval: core.Duration{Duration: 10 * time.Second},
			MaxFiles: 10000,
		},
	}
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
	if err := o.Load.validate(); err != nil {
		return err
	}
	return o.Files.validate()
}

// minFilesInterval keeps the folder scan from becoming a busy loop.
const minFilesInterval = time.Second

func (f FilesSourceOptions) validate() error {
	for _, w := range f.Watches {
		if !filepath.IsAbs(w) {
			return fmt.Errorf("files.watches: %q must be an absolute path", w)
		}
	}
	switch {
	case f.Interval.Duration < minFilesInterval:
		return errors.New("files.interval must be at least 1s")
	case f.MaxFiles < 1:
		return errors.New("files.max_files must be at least 1")
	}
	return nil
}

func (l LoadSourceOptions) validate() error {
	lowest := min(l.CPUThresholdPercent, l.MemoryThresholdPercent)
	switch {
	case l.CPUThresholdPercent < 1 || l.CPUThresholdPercent > 100:
		return errors.New("load.cpu_threshold_percent must be 1 to 100")
	case l.MemoryThresholdPercent < 1 || l.MemoryThresholdPercent > 100:
		return errors.New("load.memory_threshold_percent must be 1 to 100")
	case l.MarginPercent < 0 || l.MarginPercent >= lowest:
		return errors.New("load.margin_percent must be at least 0 and below both thresholds")
	case l.Interval.Duration <= 0:
		return errors.New("load.interval must be positive")
	case l.Window.Duration < l.Interval.Duration:
		return errors.New("load.window must be at least one interval")
	case l.ProcDir == "":
		return errors.New("load.proc_dir must not be empty")
	}
	return nil
}
