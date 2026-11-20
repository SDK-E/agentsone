// SPDX-License-Identifier: MIT

package load_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/emitters/load"
	"github.com/SDK-E/agentsone/internal/emitkit/domain"
	"github.com/SDK-E/agentsone/internal/emitkit/emitkittest"
)

// meter is a fake machine: each step sets the CPU and memory use the next
// sample will see. CPU is cumulative, as /proc/stat is.
type meter struct {
	busy, total uint64
	memPercent  int
	cpuErr      error
	memErr      error
}

func (m *meter) set(cpu, mem int) {
	m.total += 1000
	m.busy += uint64(cpu) * 10 // #nosec G115 -- test percentages are 0 to 100
	m.memPercent = mem
}

func (m *meter) CPU() (load.CPUSample, error) {
	return load.CPUSample{Busy: m.busy, Total: m.total}, m.cpuErr
}

func (m *meter) Memory() (load.MemInfo, error) {
	return load.MemInfo{TotalBytes: 1000 << 20, AvailableBytes: uint64(100-m.memPercent) * 10 << 20}, m.memErr // #nosec G115 -- test percentages are 0 to 100
}

type step struct{ cpu, mem int }

func testConfig() load.Config {
	cfg := load.DefaultConfig()
	cfg.Interval = 10 * time.Second
	cfg.Window = 30 * time.Second // three samples
	return cfg
}

// runSteps runs the source over the steps; step i is what sample i sees.
// CPU use is a difference between two samples, so sample 0 only sets the
// baseline: the first CPU reading comes with sample 1. Memory reads at once.
func runSteps(t *testing.T, m *meter, cfg load.Config, steps []step) []domain.Draft {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	clk := emitkittest.NewClock(time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC))
	i := 0
	sl := &emitkittest.Sleeper{Clock: clk, Cancel: cancel, Limit: len(steps)}
	src := load.New(cfg, m)
	m.set(steps[0].cpu, steps[0].mem)
	sl.OnSleep = func(time.Duration) {
		i++
		if i < len(steps) {
			m.set(steps[i].cpu, steps[i].mem)
		}
	}
	pub := &emitkittest.Publisher{}
	if err := src.Run(ctx, emitkittest.Deps(clk, sl, pub)); err != nil {
		t.Fatalf("Run error = %v", err)
	}
	return pub.Drafts()
}

func types(drafts []domain.Draft) string {
	var out []string
	for _, d := range drafts {
		out = append(out, strings.TrimPrefix(d.Type, "agentsone.load."))
	}
	return strings.Join(out, ",")
}

func repeat(s step, n int) []step {
	out := make([]step, n)
	for i := range out {
		out[i] = s
	}
	return out
}

func TestRun_CPUHighAfterTheWindowThenNormal(t *testing.T) {
	t.Parallel()
	steps := append(repeat(step{97, 40}, 4), repeat(step{20, 40}, 2)...)

	drafts := runSteps(t, &meter{}, testConfig(), steps)

	if got := types(drafts); got != "cpu.high,normal" {
		t.Fatalf("events = %s, want cpu.high,normal", got)
	}
	wantHigh := `{"threshold_percent":90,"top":[],"used_percent":97,"window_seconds":30}`
	if string(drafts[0].Data) != wantHigh {
		t.Fatalf("cpu.high data = %s, want %s", drafts[0].Data, wantHigh)
	}
	wantNormal := `{"cpu_percent":20,"memory_percent":40}`
	if string(drafts[1].Data) != wantNormal {
		t.Fatalf("normal data = %s, want %s", drafts[1].Data, wantNormal)
	}
	emitkittest.RequireInCatalog(t, "load", drafts)
}

func TestRun_AShortSpikeSaysNothing(t *testing.T) {
	t.Parallel()
	steps := []step{{97, 40}, {98, 40}, {20, 40}, {97, 40}, {97, 40}, {20, 40}}
	if got := types(runSteps(t, &meter{}, testConfig(), steps)); got != "" {
		t.Fatalf("events = %q, want none for spikes shorter than the window", got)
	}
}

func TestRun_MemoryHighCarriesAvailableBytes(t *testing.T) {
	t.Parallel()
	drafts := runSteps(t, &meter{}, testConfig(), repeat(step{10, 93}, 3))

	if got := types(drafts); got != "memory.high" {
		t.Fatalf("events = %s, want memory.high", got)
	}
	want := `{"available_bytes":73400320,"threshold_percent":90,"used_percent":93}`
	if string(drafts[0].Data) != want {
		t.Fatalf("data = %s, want %s", drafts[0].Data, want)
	}
	emitkittest.RequireInCatalog(t, "load", drafts)
}

func TestRun_NormalOnlyWhenBothAreBackUnderTheirThresholds(t *testing.T) {
	t.Parallel()
	steps := append(repeat(step{97, 95}, 4), // memory is high at sample 2, cpu at sample 3
		step{20, 95}, step{20, 95}, // cpu recovers, memory still high
		step{20, 40}) // memory recovers

	drafts := runSteps(t, &meter{}, testConfig(), steps)

	if got := types(drafts); got != "memory.high,cpu.high,normal" {
		t.Fatalf("events = %s, want memory.high,cpu.high and a single normal at the end", got)
	}
}

func TestRun_AnUnreadableMeterIsSurvived(t *testing.T) {
	t.Parallel()
	m := &meter{cpuErr: errors.New("read /proc/stat: permission denied"), memErr: errors.New("read /proc/meminfo: gone")}
	if got := types(runSteps(t, m, testConfig(), repeat(step{99, 99}, 5))); got != "" {
		t.Fatalf("events = %q, want none while nothing can be read", got)
	}
}

func TestRun_OneMeterFailingDoesNotHideTheOther(t *testing.T) {
	t.Parallel()
	m := &meter{cpuErr: errors.New("no /proc/stat")}
	if got := types(runSteps(t, m, testConfig(), repeat(step{99, 95}, 3))); got != "memory.high" {
		t.Fatalf("events = %q, want memory.high", got)
	}
}

func TestRun_ASampleWithNoCPUTimeElapsedIsSkipped(t *testing.T) {
	t.Parallel()
	src := load.New(testConfig(), &frozen{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	clk := emitkittest.NewClock(time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC))
	sl := &emitkittest.Sleeper{Clock: clk, Cancel: cancel, Limit: 4}
	pub := &emitkittest.Publisher{}
	if err := src.Run(ctx, emitkittest.Deps(clk, sl, pub)); err != nil {
		t.Fatal(err)
	}
	if got := types(pub.Drafts()); got != "" {
		t.Fatalf("events = %q, want none when the counters do not move", got)
	}
}

type frozen struct{}

func (frozen) CPU() (load.CPUSample, error) { return load.CPUSample{Busy: 5, Total: 10}, nil }
func (frozen) Memory() (load.MemInfo, error) {
	return load.MemInfo{TotalBytes: 0}, nil
}

func TestRun_UsesTheConfiguredThresholdsAndInterval(t *testing.T) {
	t.Parallel()
	cfg := testConfig()
	cfg.CPUThresholdPercent = 50
	cfg.Window = 10 * time.Second // one sample
	drafts := runSteps(t, &meter{}, cfg, repeat(step{60, 10}, 2))
	if got := types(drafts); got != "cpu.high" {
		t.Fatalf("events = %q, want cpu.high at a 50 percent threshold", got)
	}
}

func TestRun_AFailedPublishIsNotFatal(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	clk := emitkittest.NewClock(time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC))
	sl := &emitkittest.Sleeper{Clock: clk, Cancel: cancel, Limit: 3}
	m := &meter{}
	sl.OnSleep = func(time.Duration) { m.set(10, 95) }
	m.set(10, 95)
	pub := &emitkittest.Publisher{Fail: func(int) error { return errors.New("bus down") }}
	cfg := testConfig()
	cfg.Window = 10 * time.Second
	if err := load.New(cfg, m).Run(ctx, emitkittest.Deps(clk, sl, pub)); err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if pub.Calls() == 0 {
		t.Fatal("the source never tried to publish")
	}
}
