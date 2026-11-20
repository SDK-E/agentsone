// SPDX-License-Identifier: MIT

package daemontest_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/SDK-E/agentsone/internal/agents/adapters/sqlite"
	"github.com/SDK-E/agentsone/internal/agents/domain"
	"github.com/SDK-E/agentsone/internal/contract/eventdata"
	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/core/coretest"
	"github.com/SDK-E/agentsone/internal/daemontest"
	"github.com/SDK-E/agentsone/internal/store"
)

// crashedHire is the event id of the hire a crash interrupted.
const crashedHire = "evt_01JDCRASHEDHIRE0000000000"

// A hire stores the agent and its hired event in one transaction of the
// society's store, and publishes the event after the commit. The daemon can
// stop in two places after the commit; after a restart, in both, the agent
// is there and its event is published exactly once.
func TestHire_AHireACrashInterruptedIsAnnouncedOnceAfterTheRestart(t *testing.T) {
	t.Parallel()
	d := daemontest.Start(t)
	d.SignInHuman("alice")
	waitSeeded(t, d)

	// The daemon stops after the hire's transaction, before the publish:
	// core.db holds the agent and its pending event, the events store
	// nothing.
	d.Stop()
	commitHire(t, d, "scribe")
	d = d.Restart()

	if _, err := d.Ports.Agents.GetAgent(asDaemon(), "scribe"); err != nil {
		t.Fatalf("the interrupted hire is gone after the restart: %v", err)
	}
	founder := d.AsAgent("founder")
	if ev := hiredEvents(founder, "scribe"); len(ev) != 1 || ev[0].Actor != "agent:founder" || ev[0].Source != "/agents/founder" {
		t.Fatalf("hired events after the restart = %+v, want one from the founder", ev)
	}

	// The daemon stops after the publish, before it marks the event sent:
	// the events store has the event, core.db still lists it as pending.
	d.Stop()
	markUnsent(t, d, crashedHire)
	d = d.Restart()

	if ev := hiredEvents(d.AsAgent("founder"), "scribe"); len(ev) != 1 {
		t.Fatalf("hired events after the second restart = %+v, want still one", ev)
	}
	if n := pendingEvents(t, d); n != 0 {
		t.Fatalf("the outbox holds %d events after the restart, want none", n)
	}
}

// waitSeeded waits until the seed announced its whole team, so the test
// stops the daemon at a moment it chose.
func waitSeeded(t *testing.T, d *daemontest.Daemon) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), daemontest.Timeout)
	defer cancel()
	hired, err := d.Ports.Subscribe.Subscribe(ctx, core.EventFilter{Types: []string{eventdata.TypeAgentsHired}})
	if err != nil {
		t.Fatalf("subscribe to hires: %v", err)
	}
	waiting := map[string]bool{"router": true, "waker": true}
	for len(waiting) > 0 {
		select {
		case ev, ok := <-hired:
			if !ok {
				t.Fatalf("the seed did not hire %v within %s", waiting, daemontest.Timeout)
			}
			var data eventdata.AgentsHired
			if json.Unmarshal(ev.Data, &data) == nil {
				delete(waiting, data.Agent)
			}
		case <-ctx.Done():
			t.Fatalf("the seed did not hire %v within %s", waiting, daemontest.Timeout)
		}
	}
}

// openCore opens the stopped daemon's core.db.
func openCore(t *testing.T, d *daemontest.Daemon) *store.DB {
	t.Helper()
	db, err := store.Open(context.Background(), d.Config.DBPath("core"))
	if err != nil {
		t.Fatalf("open core.db: %v", err)
	}
	t.Cleanup(func() { coretest.Close(t, db) })
	return db
}

// commitHire writes what the hire's transaction commits: the agent below
// the founder, as the chart makes it, with its pending hired event.
func commitHire(t *testing.T, d *daemontest.Daemon, name string) {
	t.Helper()
	repo := sqlite.New(openCore(t, d))
	all, err := repo.ListAgents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	chart := domain.NewChart(all)
	spec := domain.HireSpec{Name: name, DisplayName: name, Kind: domain.KindChat, Runner: "echo", Role: "Writes things down"}
	a, err := chart.Hire("founder", spec, d.Clock.Now())
	if err != nil {
		t.Fatalf("hire %s on the chart: %v", name, err)
	}
	data, err := json.Marshal(eventdata.AgentsHired{Agent: a.Name, Manager: a.Manager, Kind: eventdata.AgentsHiredKind(a.Kind), Template: a.Template})
	if err != nil {
		t.Fatal(err)
	}
	ev := domain.Outbound{
		ID: crashedHire, Source: "/agents/founder", Actor: "agent:founder",
		ChartScope: "team:" + domain.NewChart(append(all, a)).Team(a.Name),
		Type:       eventdata.TypeAgentsHired, Data: data, CreatedAt: d.Clock.Now(),
	}
	if err := repo.HireAgent(context.Background(), a, ev); err != nil {
		t.Fatalf("commit the hire of %s: %v", name, err)
	}
}

// markUnsent puts the event back in the outbox's pending list, as when the
// daemon stopped between the publish and marking it sent.
func markUnsent(t *testing.T, d *daemontest.Daemon, id string) {
	t.Helper()
	db := openCore(t, d)
	res, err := db.SQL().ExecContext(context.Background(), `UPDATE outbox SET sent_at = NULL WHERE id = ?`, id)
	if err != nil {
		t.Fatalf("mark %s unsent: %v", id, err)
	}
	if n, err := res.RowsAffected(); err != nil || n != 1 {
		t.Fatalf("mark %s unsent: %d rows (%v), want 1", id, n, err)
	}
}

func pendingEvents(t *testing.T, d *daemontest.Daemon) int {
	t.Helper()
	// The daemon runs, so read through a second connection to core.db.
	got, err := sqlite.New(openCore(t, d)).PendingEvents(context.Background(), 100)
	if err != nil {
		t.Fatal(err)
	}
	return len(got)
}
