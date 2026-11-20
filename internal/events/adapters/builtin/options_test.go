// SPDX-License-Identifier: MIT

package builtin_test

import (
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/events/adapters/builtin"
)

func TestLoadOptions_DefaultsWhenTheTableIsAbsent(t *testing.T) {
	t.Parallel()
	cfg := core.DefaultConfig()

	got, err := builtin.LoadOptions(&cfg)

	if err != nil {
		t.Fatalf("LoadOptions error = %v", err)
	}
	if d := got.Disk; d.ThresholdPercent != 90 || d.MarginPercent != 2 || d.Interval.Duration != time.Minute || d.MountsFile != "/proc/self/mounts" {
		t.Fatalf("disk options = %+v", d)
	}
}

func TestLoadOptions_ReadsTheEmittersTable(t *testing.T) {
	t.Parallel()
	cfg, err := core.ParseConfig([]byte(`
[emitters.disk]
threshold_percent = 80
margin_percent = 5
interval = "30s"
mounts_file = "/tmp/mounts"
`))
	if err != nil {
		t.Fatal(err)
	}

	got, err := builtin.LoadOptions(&cfg)

	if err != nil {
		t.Fatalf("LoadOptions error = %v", err)
	}
	if d := got.Disk; d.ThresholdPercent != 80 || d.MarginPercent != 5 || d.Interval.Duration != 30*time.Second || d.MountsFile != "/tmp/mounts" {
		t.Fatalf("disk options = %+v", d)
	}
}

func TestLoadOptions_UnknownKeysAreAnError(t *testing.T) {
	t.Parallel()
	cfg, err := core.ParseConfig([]byte("[emitters.disk]\nlimit_percent = 80\n"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := builtin.LoadOptions(&cfg); err == nil {
		t.Fatal("LoadOptions error = nil, want the typo reported")
	}
}

func TestLoadOptions_RejectsValuesThatCannotWork(t *testing.T) {
	t.Parallel()
	for name, toml := range map[string]string{
		"threshold above 100": "[emitters.disk]\nthreshold_percent = 101\n",
		"threshold zero":      "[emitters.disk]\nthreshold_percent = 0\n",
		"negative margin":     "[emitters.disk]\nmargin_percent = -1\n",
		"zero interval":       "[emitters.disk]\ninterval = \"0s\"\n",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			cfg, err := core.ParseConfig([]byte(toml))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := builtin.LoadOptions(&cfg); err == nil {
				t.Fatal("LoadOptions error = nil, want one")
			}
		})
	}
}

func TestLoadOptions_LoadTableDefaultsAndOverrides(t *testing.T) {
	t.Parallel()
	cfg := core.DefaultConfig()
	got, err := builtin.LoadOptions(&cfg)
	if err != nil {
		t.Fatal(err)
	}
	l := got.Load
	if l.CPUThresholdPercent != 90 || l.MemoryThresholdPercent != 90 || l.MarginPercent != 5 ||
		l.Interval.Duration != 10*time.Second || l.Window.Duration != time.Minute || l.ProcDir != "/proc" {
		t.Fatalf("load defaults = %+v", l)
	}

	set, err := core.ParseConfig([]byte(`
[emitters.load]
cpu_threshold_percent = 80
memory_threshold_percent = 70
margin_percent = 3
interval = "5s"
window = "20s"
proc_dir = "/x"
`))
	if err != nil {
		t.Fatal(err)
	}
	got, err = builtin.LoadOptions(&set)
	if err != nil {
		t.Fatal(err)
	}
	l = got.Load
	if l.CPUThresholdPercent != 80 || l.MemoryThresholdPercent != 70 || l.MarginPercent != 3 ||
		l.Interval.Duration != 5*time.Second || l.Window.Duration != 20*time.Second || l.ProcDir != "/x" {
		t.Fatalf("load options = %+v", l)
	}
}

func TestLoadOptions_RejectsLoadValuesThatCannotWork(t *testing.T) {
	t.Parallel()
	for name, toml := range map[string]string{
		"cpu threshold zero":       "[emitters.load]\ncpu_threshold_percent = 0\n",
		"memory threshold over":    "[emitters.load]\nmemory_threshold_percent = 101\n",
		"margin not below":         "[emitters.load]\nmargin_percent = 90\n",
		"zero interval":            "[emitters.load]\ninterval = \"0s\"\n",
		"window shorter than step": "[emitters.load]\ninterval = \"30s\"\nwindow = \"10s\"\n",
		"empty proc dir":           "[emitters.load]\nproc_dir = \"\"\n",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			cfg, err := core.ParseConfig([]byte(toml))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := builtin.LoadOptions(&cfg); err == nil {
				t.Fatal("LoadOptions error = nil, want one")
			}
		})
	}
}

func TestLoadOptions_FilesTableDefaultsAndOverrides(t *testing.T) {
	t.Parallel()
	cfg := core.DefaultConfig()
	got, err := builtin.LoadOptions(&cfg)
	if err != nil {
		t.Fatal(err)
	}
	if f := got.Files; len(f.Watches) != 0 || f.Interval.Duration != 10*time.Second || f.MaxFiles != 10000 {
		t.Fatalf("files defaults = %+v, want no watches", f)
	}

	set, err := core.ParseConfig([]byte("[emitters.files]\nwatches = [\"/srv/inbox\", \"/etc/agentsone\"]\ninterval = \"30s\"\nmax_files = 50\n"))
	if err != nil {
		t.Fatal(err)
	}
	got, err = builtin.LoadOptions(&set)
	if err != nil {
		t.Fatal(err)
	}
	if f := got.Files; len(f.Watches) != 2 || f.Watches[0] != "/srv/inbox" || f.Interval.Duration != 30*time.Second || f.MaxFiles != 50 {
		t.Fatalf("files options = %+v", f)
	}
}

func TestLoadOptions_RejectsFilesValuesThatCannotWork(t *testing.T) {
	t.Parallel()
	for name, toml := range map[string]string{
		"relative watch": "[emitters.files]\nwatches = [\"inbox\"]\n",
		"empty watch":    "[emitters.files]\nwatches = [\"\"]\n",
		"zero interval":  "[emitters.files]\ninterval = \"0s\"\n",
		"fast interval":  "[emitters.files]\ninterval = \"100ms\"\n",
		"zero max files": "[emitters.files]\nmax_files = 0\n",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			cfg, err := core.ParseConfig([]byte(toml))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := builtin.LoadOptions(&cfg); err == nil {
				t.Fatal("LoadOptions error = nil, want one")
			}
		})
	}
}

func TestLoadOptions_NilConfigGivesDefaults(t *testing.T) {
	t.Parallel()
	got, err := builtin.LoadOptions(nil)
	if err != nil || got.Disk.ThresholdPercent != 90 {
		t.Fatalf("LoadOptions(nil) = %+v, %v", got, err)
	}
}
