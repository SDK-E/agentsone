// SPDX-License-Identifier: MIT

package daemontest_test

import (
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/contract/api"
	"github.com/SDK-E/agentsone/internal/daemontest"
)

// System tests, one per built-in event source: the real source runs inside
// the real daemon, publishes its event on the bus, and a trigger wakes the
// agent that watches for it, through the supervisor and the runner socket
// to the real echo runner.

func TestClockSource_TheMinuteTickWakesTheWatchingAgent(t *testing.T) {
	t.Parallel()
	d := daemontest.Start(t)
	out := watch(t, d, "agentsone.clock.tick")

	d.MoveSources(time.Minute)

	out.WaitText("echo: event agentsone.clock.tick")
}

func TestDiskSource_AFullDiskWakesTheWatchingAgent(t *testing.T) {
	t.Parallel()
	d := daemontest.Start(t)
	out := watch(t, d, "agentsone.disk.usage.high")

	d.MountDisk(d.Config.StateDir)
	d.MoveSources(time.Minute)

	out.WaitText("echo: event agentsone.disk.usage.high")
}

func TestLoadSource_FullMemoryWakesTheWatchingAgent(t *testing.T) {
	t.Parallel()
	d := daemontest.Start(t)
	out := watch(t, d, "agentsone.load.memory.high")

	d.FillMemory()
	d.MoveSources(10 * time.Second)

	out.WaitText("echo: event agentsone.load.memory.high")
}

func TestFilesSource_ANewFileWakesTheWatchingAgent(t *testing.T) {
	t.Parallel()
	d := daemontest.Start(t)
	out := watch(t, d, "agentsone.files.created")

	d.DropFile("report.txt", "Q3 numbers")
	d.MoveSources(10 * time.Second) // the scan that sees it
	d.MoveSources(10 * time.Second) // the scan that sees it unchanged

	out.WaitText("echo: event agentsone.files.created")
}

// watch hires the echo agent, has the founder wake it on events of type,
// and returns the agent's output as a human sees it.
func watch(t *testing.T, d *daemontest.Daemon, typ string) *daemontest.Stream {
	t.Helper()
	alice := d.SignInHuman("alice")
	founder := d.AsAgent("founder")
	founder.HireEcho("echo")
	out := alice.Output("echo")
	var action api.TriggerAction
	if err := action.FromWakeAction(api.WakeAction{Kind: api.WakeActionKindWake, Agent: "echo", Message: "{{type}} happened"}); err != nil {
		t.Fatal(err)
	}
	types := []string{typ}
	founder.CreateTrigger(api.TriggerCreate{
		Name: "watch", Filter: api.EventFilter{Types: &types}, Action: action,
		WakeRate: api.WakeRate{Max: 5, PerSeconds: 60},
	})
	return out
}
