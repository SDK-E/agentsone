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

func TestLoadOptions_NilConfigGivesDefaults(t *testing.T) {
	t.Parallel()
	got, err := builtin.LoadOptions(nil)
	if err != nil || got.Disk.ThresholdPercent != 90 {
		t.Fatalf("LoadOptions(nil) = %+v, %v", got, err)
	}
}
