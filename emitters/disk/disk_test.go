// SPDX-License-Identifier: MIT

package disk_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/emitters/disk"
	"github.com/SDK-E/agentsone/internal/emitkit/domain"
	"github.com/SDK-E/agentsone/internal/emitkit/emitkittest"
)

const gib = 1 << 30

// fsys is a fake file system table whose usage the test changes between samples.
type fsys struct {
	mounts  []disk.Mount
	usage   map[string]disk.Usage
	listErr error
}

func (f *fsys) List() ([]disk.Mount, error) { return f.mounts, f.listErr }

func (f *fsys) Usage(path string) (disk.Usage, error) {
	u, ok := f.usage[path]
	if !ok {
		return disk.Usage{}, errors.New("statfs " + path + ": no such file or directory")
	}
	return u, nil
}

// at returns a usage with the given percent used of a 100 GiB file system.
func at(percent int) disk.Usage {
	used := uint64(percent) // #nosec G115 -- test percentages are 0 to 100
	return disk.Usage{TotalBytes: 100 * gib, UsedBytes: used * gib, AvailBytes: (100 - used) * gib}
}

// sample runs the source for len(readings) samples; readings[i] is applied before sample i.
func sample(t *testing.T, f *fsys, readings []map[string]int, cfg disk.Config) []domain.Draft {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	clk := emitkittest.NewClock(time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC))
	i := 0
	sl := &emitkittest.Sleeper{Clock: clk, Cancel: cancel, Limit: len(readings)}
	apply := func(r map[string]int) {
		for path, pct := range r {
			f.usage[path] = at(pct)
		}
	}
	apply(readings[0])
	sl.OnSleep = func(time.Duration) {
		i++
		if i < len(readings) {
			apply(readings[i])
		}
	}
	pub := &emitkittest.Publisher{}
	src := disk.New(cfg, f, f)
	if err := src.Run(ctx, emitkittest.Deps(clk, sl, pub)); err != nil {
		t.Fatalf("Run error = %v", err)
	}
	return pub.Drafts()
}

func newFS(paths ...string) *fsys {
	f := &fsys{usage: map[string]disk.Usage{}}
	for _, p := range paths {
		f.mounts = append(f.mounts, disk.Mount{Path: p, FSType: "ext4"})
	}
	return f
}

func describe(drafts []domain.Draft) []string {
	var out []string
	for _, d := range drafts {
		out = append(out, strings.TrimPrefix(d.Type, "agentsone.disk.")+" "+d.Subject)
	}
	return out
}

func requireEvents(t *testing.T, drafts []domain.Draft, want ...string) {
	t.Helper()
	got := describe(drafts)
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("events = %v, want %v", got, want)
	}
}

func TestRun_OneEventPerTransitionNotPerSample(t *testing.T) {
	t.Parallel()
	f := newFS("/var")
	drafts := sample(t, f, []map[string]int{
		{"/var": 50}, {"/var": 91}, {"/var": 95}, {"/var": 90}, {"/var": 80}, {"/var": 81},
	}, disk.DefaultConfig())

	requireEvents(t, drafts, "usage.high /var", "usage.normal /var")
	emitkittest.RequireInCatalog(t, "disk", drafts)
}

func TestRun_HighEventCarriesTheContractFields(t *testing.T) {
	t.Parallel()
	drafts := sample(t, newFS("/var"), []map[string]int{{"/var": 91}}, disk.DefaultConfig())
	if len(drafts) != 1 {
		t.Fatalf("events = %v, want one", describe(drafts))
	}
	want := `{"free_bytes":9663676416,"mount":"/var","threshold_percent":90,"used_percent":91}`
	if string(drafts[0].Data) != want {
		t.Fatalf("data = %s, want %s", drafts[0].Data, want)
	}
}

func TestRun_StartingNormalSaysNothing(t *testing.T) {
	t.Parallel()
	drafts := sample(t, newFS("/", "/var"), []map[string]int{{"/": 10, "/var": 20}, {"/": 11, "/var": 21}}, disk.DefaultConfig())
	requireEvents(t, drafts)
}

func TestRun_MountsAreIndependent(t *testing.T) {
	t.Parallel()
	drafts := sample(t, newFS("/", "/var"), []map[string]int{
		{"/": 10, "/var": 95}, {"/": 96, "/var": 95}, {"/": 96, "/var": 50},
	}, disk.DefaultConfig())
	requireEvents(t, drafts, "usage.high /var", "usage.high /", "usage.normal /var")
}

func TestRun_UsesTheConfiguredThreshold(t *testing.T) {
	t.Parallel()
	cfg := disk.DefaultConfig()
	cfg.ThresholdPercent = 70
	drafts := sample(t, newFS("/"), []map[string]int{{"/": 69}, {"/": 70}}, cfg)
	requireEvents(t, drafts, "usage.high /")
}

func TestRun_SkipsPseudoFileSystemsAndDuplicates(t *testing.T) {
	t.Parallel()
	f := &fsys{usage: map[string]disk.Usage{}, mounts: []disk.Mount{
		{Path: "/proc", FSType: "proc"},
		{Path: "/dev/shm", FSType: "tmpfs"},
		{Path: "/sys/fs/cgroup", FSType: "cgroup2"},
		{Path: "/", FSType: "overlay"},
		{Path: "/", FSType: "overlay"},
		{Path: "/data", FSType: "ext4"},
	}}
	f.usage["/proc"], f.usage["/dev/shm"], f.usage["/sys/fs/cgroup"] = at(99), at(99), at(99)
	drafts := sample(t, f, []map[string]int{{"/": 95, "/data": 95}}, disk.DefaultConfig())
	requireEvents(t, drafts, "usage.high /", "usage.high /data")
}

func TestRun_AMountThatCannotBeReadIsSkippedNotFatal(t *testing.T) {
	t.Parallel()
	f := newFS("/gone", "/var")
	drafts := sample(t, f, []map[string]int{{"/var": 95}, {"/var": 95}}, disk.DefaultConfig())
	requireEvents(t, drafts, "usage.high /var")
}

func TestRun_AFailingMountListIsSurvived(t *testing.T) {
	t.Parallel()
	f := newFS("/var")
	f.listErr = errors.New("read mounts: permission denied")
	drafts := sample(t, f, []map[string]int{{"/var": 95}, {"/var": 95}}, disk.DefaultConfig())
	requireEvents(t, drafts)
}

func TestRun_AZeroSizeFileSystemIsIgnored(t *testing.T) {
	t.Parallel()
	f := newFS("/empty")
	f.usage["/empty"] = disk.Usage{}
	drafts := sample(t, f, []map[string]int{{}, {}}, disk.DefaultConfig())
	requireEvents(t, drafts)
}

func TestRun_AMountThatVanishesForgetsItsState(t *testing.T) {
	t.Parallel()
	f := newFS("/mnt/usb")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	clk := emitkittest.NewClock(time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC))
	step := 0
	sl := &emitkittest.Sleeper{Clock: clk, Cancel: cancel, Limit: 3}
	sl.OnSleep = func(time.Duration) {
		step++
		switch step {
		case 1:
			f.mounts = nil // unplugged while high
		case 2:
			f.mounts = []disk.Mount{{Path: "/mnt/usb", FSType: "ext4"}} // plugged in again, still full
		}
	}
	f.usage["/mnt/usb"] = at(95)
	pub := &emitkittest.Publisher{}

	if err := disk.New(disk.DefaultConfig(), f, f).Run(ctx, emitkittest.Deps(clk, sl, pub)); err != nil {
		t.Fatalf("Run error = %v", err)
	}

	requireEvents(t, pub.Drafts(), "usage.high /mnt/usb", "usage.high /mnt/usb")
}

func TestRun_SamplesOnTheConfiguredInterval(t *testing.T) {
	t.Parallel()
	cfg := disk.DefaultConfig()
	cfg.Interval = 7 * time.Second
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	clk := emitkittest.NewClock(time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC))
	sl := &emitkittest.Sleeper{Clock: clk, Cancel: cancel, Limit: 1}
	f := newFS("/")
	f.usage["/"] = at(10)
	if err := disk.New(cfg, f, f).Run(ctx, emitkittest.Deps(clk, sl, &emitkittest.Publisher{})); err != nil {
		t.Fatal(err)
	}
	if got := sl.Slept(); len(got) != 1 || got[0] != 7*time.Second {
		t.Fatalf("slept = %v, want [7s]", got)
	}
}

func TestPublishFailureIsLoggedAndTheNextTransitionStillGoesOut(t *testing.T) {
	t.Parallel()
	f := newFS("/var")
	f.usage["/var"] = at(95)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	clk := emitkittest.NewClock(time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC))
	sl := &emitkittest.Sleeper{Clock: clk, Cancel: cancel, Limit: 2}
	sl.OnSleep = func(time.Duration) { f.usage["/var"] = at(10) }
	pub := &emitkittest.Publisher{Fail: func(n int) error {
		if n == 1 {
			return errors.New("daemon restarting")
		}
		return nil
	}}
	if err := disk.New(disk.DefaultConfig(), f, f).Run(ctx, emitkittest.Deps(clk, sl, pub)); err != nil {
		t.Fatal(err)
	}
	requireEvents(t, pub.Drafts(), "usage.normal /var")
}
