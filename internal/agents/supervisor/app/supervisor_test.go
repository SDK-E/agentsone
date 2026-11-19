// SPDX-License-Identifier: MIT

package app_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/SDK-E/agentsone/internal/agents/supervisor/app"
	"github.com/SDK-E/agentsone/internal/agents/supervisor/domain"
	"github.com/SDK-E/agentsone/internal/contract/common"
	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/runners/sdk/protocol"
)

func TestProvision_CreatesUserAndInstallsLimitedUnitForCustomAgent(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	mira, _ := w.fakes.Agents.GetAgent(t.Context(), "mira")

	uid, err := w.sup.Provision(t.Context(), mira, nil)
	if err != nil {
		t.Fatalf("Provision() error = %v", err)
	}
	if uid != 200001 {
		t.Errorf("uid = %d, want the stored 200001", uid)
	}
	if acc := w.accounts.created["mira"]; acc.Home != "/home/agents/mira" || acc.Shell != domain.NoLoginShell {
		t.Errorf("account = %+v", acc)
	}
	u, ok := w.launcher.unit("mira")
	if !ok || u.Limits != domain.DefaultCustomLimits || u.Account.User != "mira" {
		t.Fatalf("installed unit = %+v, %v", u, ok)
	}
	if !slices.Equal(u.Exec.Args, []string{"--agent", "mira"}) || u.Exec.Env[0] != "ECHO_SOCKET=/run/agentsone/runners/mira.sock" {
		t.Errorf("exec = %+v", u.Exec)
	}
	if w.launcher.isRunning("mira") {
		t.Error("Provision started the runner, want installed only")
	}
	if got := w.sup.LimitMode("mira"); got != domain.LimitsHard {
		t.Errorf("LimitMode = %s, want the launcher's hard", got)
	}
	if diff := cmp.Diff([]string{"agent.runner.provision agent:mira"}, w.actions()); diff != "" {
		t.Errorf("audit (-want +got):\n%s", diff)
	}
}

func TestProvision_RootAgentRunsAsRootWithoutUserOrLimits(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	founder, _ := w.fakes.Agents.GetAgent(t.Context(), "founder")

	uid, err := w.sup.Provision(t.Context(), founder, nil)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := w.launcher.unit("founder")
	if uid != 0 || len(w.accounts.created) != 0 || u.Account.User != "root" || u.Limits != (domain.Limits{}) {
		t.Fatalf("uid=%d accounts=%v unit=%+v", uid, w.accounts.created, u)
	}
}

// After the user database was reset, a known agent is recreated with its stored uid.
func TestProvision_RecreatesUserWithStoredUID(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	mira, _ := w.fakes.Agents.GetAgent(t.Context(), "mira")
	mira.UID = 200011
	if _, err := w.sup.Provision(t.Context(), mira, nil); err != nil {
		t.Fatal(err)
	}
	if got := w.accounts.created["mira"].UID; got != 200011 {
		t.Fatalf("account uid = %d, want the stored 200011", got)
	}
}

// The society context allocates uids; the supervisor never does.
func TestProvision_RefusesCustomAgentWithoutUID(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	_, err := w.sup.Provision(t.Context(), core.Agent{Name: "nova", Runner: "echo"}, nil)
	if !errors.Is(err, core.ErrInvalid) || len(w.accounts.created) != 0 {
		t.Fatalf("error = %v accounts = %v, want ErrInvalid and no account", err, w.accounts.created)
	}
}

func TestProvision_UsesGivenLimits(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	mira, _ := w.fakes.Agents.GetAgent(t.Context(), "mira")
	limits := domain.Limits{CPUQuotaPercent: 25, MemoryMaxBytes: 1 << 28, TasksMax: 32}
	if _, err := w.sup.Provision(t.Context(), mira, &limits); err != nil {
		t.Fatal(err)
	}
	if u, _ := w.launcher.unit("mira"); u.Limits != limits {
		t.Fatalf("limits = %+v, want %+v", u.Limits, limits)
	}
}

func TestProvision_RefusesAndLeavesNothingBehind(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		agent   core.Agent
		wantErr error
	}{
		{"name with a slash", core.Agent{Name: "../etc", Runner: "echo"}, core.ErrInvalid},
		{"unknown runner", core.Agent{Name: "nova", Runner: "nope", UID: 200009}, core.ErrNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			w := newWorld(t)
			_, err := w.sup.Provision(t.Context(), tt.agent, nil)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Provision() error = %v, want %v", err, tt.wantErr)
			}
			if _, ok := w.launcher.unit(tt.agent.Name); ok || len(w.fakes.Audit.Entries()) != 0 {
				t.Fatal("refused provision installed a unit or wrote audit")
			}
		})
	}
}

func TestWake_StartsStoppedAgentAndSendsWakeAfterFirstIdle(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	payload := json.RawMessage(`{"id":"msg_1","chatId":"cht_1","from":"human:alice","text":"Hello","sentAt":"2026-11-19T19:00:01Z"}`)

	if err := w.sup.Wake(t.Context(), "founder", core.WakeChat, payload); err != nil {
		t.Fatalf("Wake() error = %v", err)
	}
	if !w.launcher.isRunning("founder") || w.sup.SessionState("founder") != domain.StateStarting {
		t.Fatalf("running=%v state=%s, want started", w.launcher.isRunning("founder"), w.sup.SessionState("founder"))
	}
	_, conn, _ := w.connect(t, "founder")

	got := conn.next(t)
	var p struct {
		Reason  string          `json:"reason"`
		TurnID  string          `json:"turnId"`
		Payload json.RawMessage `json:"payload"`
	}
	_ = json.Unmarshal(got.params, &p)
	if got.method != "wake" || p.Reason != "chat" || !strings.HasPrefix(p.TurnID, "trn_") || string(p.Payload) != string(payload) {
		t.Fatalf("request = %s %s", got.method, got.params)
	}
}

func TestWake_SendsAtOnceToReadyRunner(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	_ = w.sup.Wake(t.Context(), "founder", core.WakeTimer, json.RawMessage(`{}`))
	_, conn, _ := w.connect(t, "founder")
	conn.next(t)

	if err := w.sup.Wake(t.Context(), "founder", core.WakeMessage, json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	if got := conn.next(t); got.method != "wake" {
		t.Fatalf("request = %s, want wake", got.method)
	}
}

func TestWake_HoldsWakesWhileStarting(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	_ = w.sup.Wake(t.Context(), "founder", core.WakeTimer, json.RawMessage(`{}`))
	_ = w.sup.Wake(t.Context(), "founder", core.WakeTask, json.RawMessage(`{}`))
	_, conn, _ := w.connect(t, "founder")
	first, second := conn.next(t), conn.next(t)
	if !strings.Contains(string(first.params), `"timer"`) || !strings.Contains(string(second.params), `"task"`) {
		t.Fatalf("held wakes = %s, %s; want timer then task", first.params, second.params)
	}
}

func TestWake_RefusesFrozenAgent(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		scope core.FreezeScope
		agent string
	}{
		{"freeze all", core.FreezeScope{Kind: core.FreezeAll}, "mira"},
		{"freeze the agent", core.FreezeScope{Kind: core.FreezeAgent, Target: "mira"}, "mira"},
		{"freeze its team", core.FreezeScope{Kind: core.FreezeTeam, Target: "founder"}, "mira"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			w := newWorld(t)
			if err := w.sup.Freeze(t.Context(), tt.scope); err != nil {
				t.Fatal(err)
			}
			err := w.sup.Wake(t.Context(), tt.agent, core.WakeChat, json.RawMessage(`{}`))
			if !errors.Is(err, core.ErrFrozen) {
				t.Fatalf("Wake() error = %v, want ErrFrozen", err)
			}
			if w.launcher.isRunning(tt.agent) {
				t.Fatal("frozen agent's runner was started")
			}
		})
	}
}

func TestWake_AllowsOtherAgentsAndReleasedAgents(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	scope := core.FreezeScope{Kind: core.FreezeAgent, Target: "mira"}
	_ = w.sup.Freeze(t.Context(), scope)
	if err := w.sup.Wake(t.Context(), "watcher", core.WakeEvent, json.RawMessage(`{}`)); err != nil {
		t.Fatalf("unfrozen agent refused: %v", err)
	}
	if got, _ := w.sup.State(t.Context()); len(got) != 1 || got[0] != scope {
		t.Fatalf("freeze state = %v", got)
	}
	_ = w.sup.Release(t.Context(), scope)
	if err := w.sup.Wake(t.Context(), "mira", core.WakeChat, json.RawMessage(`{}`)); err != nil {
		t.Fatalf("released agent refused: %v", err)
	}
	if diff := cmp.Diff([]string{"agent.freeze agent:mira", "agent.release agent:mira"}, w.actions()); diff != "" {
		t.Errorf("audit (-want +got):\n%s", diff)
	}
}

func TestWake_RefusesBadNameAndReportsStartFailure(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	if err := w.sup.Wake(t.Context(), "Bad/Name", core.WakeChat, nil); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("bad name error = %v, want ErrInvalid", err)
	}
	w.launcher.startErr = errors.New("unit failed")
	if err := w.sup.Wake(t.Context(), "mira", core.WakeChat, nil); err == nil {
		t.Fatal("start failure not reported")
	}
	if w.sup.SessionState("mira") != domain.StateFailed {
		t.Fatalf("state = %s, want failed", w.sup.SessionState("mira"))
	}
}

// The gateway token comes from the daemon's one minter, for the agent's
// chart path and defence flag, with the default lifetime.
func TestHello_GatewayTokenIsMintedForTheAgentsChartPathAndDefence(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.fakes.Budget.Set(core.Budget{Agent: "mira", Defence: true})
	_ = w.sup.Wake(t.Context(), "mira", core.WakeChat, json.RawMessage(`{}`))

	_, _, res := w.connect(t, "mira")

	if res.Model.Token != "fake-gateway-token-mira" {
		t.Fatalf("model token = %q", res.Model.Token)
	}
	want := core.GatewayGrant{Subject: "mira", ChartPath: "founder/mira", Defence: true, TTL: 10 * time.Minute}
	grants := w.fakes.GatewayTokens.Grants()
	if len(grants) == 0 || grants[len(grants)-1] != want {
		t.Fatalf("grants = %+v, want last %+v", grants, want)
	}
}

// Without the model gateway the runner is not started, and the wake says why.
func TestWake_GatewayNotReachableDoesNotStartTheRunner(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.fakes.GatewayTokens.Err = fmt.Errorf("model gateway not reachable: the gateway key file is missing: %w", core.ErrUnavailable)

	err := w.sup.Wake(t.Context(), "mira", core.WakeChat, json.RawMessage(`{}`))

	if !errors.Is(err, core.ErrUnavailable) || !strings.Contains(err.Error(), "model gateway not reachable") {
		t.Fatalf("Wake error = %v, want ErrUnavailable saying the model gateway is not reachable", err)
	}
	if w.launcher.isRunning("mira") {
		t.Fatal("runner started without the model gateway")
	}
	if w.sup.SessionState("mira") != domain.StateFailed {
		t.Fatalf("state = %s, want failed", w.sup.SessionState("mira"))
	}
	if diff := cmp.Diff([]string{"agent.runner.error agent:mira"}, w.actions()); diff != "" {
		t.Errorf("audit (-want +got):\n%s", diff)
	}
	w.fakes.GatewayTokens.Err = nil
	if err := w.sup.Wake(t.Context(), "mira", core.WakeChat, json.RawMessage(`{}`)); err != nil || !w.launcher.isRunning("mira") {
		t.Fatalf("wake after the gateway came back: %v, running %v", err, w.launcher.isRunning("mira"))
	}
}

// The society hears why a runner cannot run, in plain words without the
// error's detail, and hears when it runs again.
func TestWake_TellsTheSocietyWhyTheRunnerCannotRunAndWhenItRunsAgain(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.fakes.GatewayTokens.Err = fmt.Errorf("model gateway not reachable: the gateway key file is missing (/var/lib/agentsone/gateway.key): %w", core.ErrUnavailable)
	_ = w.sup.Wake(t.Context(), "mira", core.WakeChat, json.RawMessage(`{}`))

	if diff := cmp.Diff([]string{"failed mira: The model gateway is not reachable."}, w.sink.heard()); diff != "" {
		t.Fatalf("society heard (-want +got):\n%s", diff)
	}

	w.fakes.GatewayTokens.Err = nil
	if err := w.sup.Wake(t.Context(), "mira", core.WakeChat, json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	w.connect(t, "mira")
	if diff := cmp.Diff([]string{"failed mira: The model gateway is not reachable.", "running mira"}, w.sink.heard()); diff != "" {
		t.Fatalf("society heard (-want +got):\n%s", diff)
	}
}

func TestWake_ReasonForEachWayARunnerCannotRun(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		setup func(w *world)
		want  string
	}{
		"start fails": {func(w *world) {
			w.launcher.startErr = errors.New("exec /usr/lib/agentsone/runners/echo: permission denied")
		}, "The runner could not be started."},
		"chart loops": {func(w *world) {
			w.fakes.Agents.Add(core.Agent{Name: "mira", Kind: core.AgentChat, Manager: "mira", Runner: "echo", UID: 200001})
		}, "The chart loops above this agent, so it cannot reach the model gateway."},
		"gateway answers oddly": {func(w *world) {
			w.fakes.GatewayTokens.Err = errors.New("sign: bad curve")
		}, "The model gateway token could not be issued."},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			w := newWorld(t)
			tt.setup(w)
			_ = w.sup.Wake(t.Context(), "mira", core.WakeChat, nil)
			if diff := cmp.Diff([]string{"failed mira: " + tt.want}, w.sink.heard()); diff != "" {
				t.Fatalf("society heard (-want +got):\n%s", diff)
			}
		})
	}
}

// A runner that restarted on its own while the gateway went away gets no
// welcome, and the society hears why.
func TestHello_GatewayNotReachableTellsTheSociety(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	_ = w.sup.Wake(t.Context(), "mira", core.WakeChat, json.RawMessage(`{}`))
	w.fakes.GatewayTokens.Err = fmt.Errorf("model gateway not reachable: %w", core.ErrUnavailable)
	h := w.sup.Connect("mira")
	h.Attach(newFakeConn())

	if _, err := h.Hello(t.Context(), "1.0", helloParams); !errors.Is(err, core.ErrUnavailable) {
		t.Fatalf("Hello() error = %v, want ErrUnavailable", err)
	}
	if diff := cmp.Diff([]string{"failed mira: The model gateway is not reachable."}, w.sink.heard()); diff != "" {
		t.Fatalf("society heard (-want +got):\n%s", diff)
	}
}

// A runner that reports state error has stopped with an error.
func TestStatus_ErrorStateTellsTheSociety(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	h, _, turn := startTurn(t, w, "founder")
	// The end of a turn is not the runner running again.
	if err := h.Status(t.Context(), protocol.StatusParams{State: "idle", TurnID: &turn}); err != nil {
		t.Fatal(err)
	}
	_ = h.Status(t.Context(), protocol.StatusParams{State: "error"})
	if diff := cmp.Diff([]string{"running founder", "failed founder: The runner stopped with an error."}, w.sink.heard()); diff != "" {
		t.Fatalf("society heard (-want +got):\n%s", diff)
	}
}

// Run starts an always-on agent's runner with no turn: after the handshake
// the runner idles, so it makes no model call until a wake.
func TestRun_StartsTheRunnerIdleWithoutAWake(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	if err := w.sup.Run(t.Context(), "founder"); err != nil {
		t.Fatal(err)
	}
	if !w.launcher.isRunning("founder") {
		t.Fatal("runner not started")
	}
	h, conn, _ := w.connect(t, "founder")
	w.fakes.Clock.Advance(time.Second)
	h.Heard()
	if w.sup.SessionState("founder") != domain.StateIdle {
		t.Fatalf("state = %s, want idle", w.sup.SessionState("founder"))
	}
	select {
	case c := <-conn.calls:
		t.Fatalf("runner got %s, want nothing until a wake", c.method)
	case <-time.After(50 * time.Millisecond):
	}

	// A second Run leaves the running runner alone.
	if err := w.sup.Run(t.Context(), "founder"); err != nil || w.launcher.restarts["founder"] != 0 {
		t.Fatalf("second Run = %v, restarts %d", err, w.launcher.restarts["founder"])
	}
}

func TestRun_LeavesAFrozenAgentAndReportsAMissingGateway(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	scope := core.FreezeScope{Kind: core.FreezeAgent, Target: "founder"}
	_ = w.sup.Freeze(t.Context(), scope)
	if err := w.sup.Run(t.Context(), "founder"); err != nil || w.launcher.isRunning("founder") {
		t.Fatalf("Run of a frozen agent = %v, running %v; want nothing started", err, w.launcher.isRunning("founder"))
	}
	_ = w.sup.Release(t.Context(), scope)

	w.fakes.GatewayTokens.Err = fmt.Errorf("model gateway not reachable: %w", core.ErrUnavailable)
	if err := w.sup.Run(t.Context(), "founder"); !errors.Is(err, core.ErrUnavailable) || w.launcher.isRunning("founder") {
		t.Fatalf("Run without the gateway = %v, running %v", err, w.launcher.isRunning("founder"))
	}
	if diff := cmp.Diff([]string{"failed founder: The model gateway is not reachable."}, w.sink.heard()); diff != "" {
		t.Fatalf("society heard (-want +got):\n%s", diff)
	}
	if err := w.sup.Run(t.Context(), "Bad/Name"); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("bad name = %v, want ErrInvalid", err)
	}
}

// A chart with a manager loop gets no token instead of a hang.
func TestWake_ChartLoopGetsNoGatewayToken(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.fakes.Agents.Add(core.Agent{Name: "ann", Kind: core.AgentChat, Manager: "bob", Runner: "echo", UID: 200010})
	w.fakes.Agents.Add(core.Agent{Name: "bob", Kind: core.AgentChat, Manager: "ann", Runner: "echo", UID: 200011})

	if err := w.sup.Wake(t.Context(), "ann", core.WakeChat, nil); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("Wake error = %v, want ErrInvalid", err)
	}
	if w.launcher.isRunning("ann") || len(w.fakes.GatewayTokens.Grants()) != 0 {
		t.Fatal("runner started or token minted for a looping chart")
	}
}

func TestFreeze_PutsRunningAgentToSleep(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	_ = w.sup.Wake(t.Context(), "mira", core.WakeChat, json.RawMessage(`{}`))
	_, conn, _ := w.connect(t, "mira")
	conn.next(t) // the wake

	if err := w.sup.Freeze(t.Context(), core.FreezeScope{Kind: core.FreezeAll}); err != nil {
		t.Fatal(err)
	}
	var methods []string
	for range 2 {
		methods = append(methods, conn.next(t).method)
	}
	if diff := cmp.Diff([]string{"sleep", "shutdown"}, methods); diff != "" {
		t.Errorf("requests (-want +got):\n%s", diff)
	}
	if w.launcher.isRunning("mira") || w.sup.SessionState("mira") != domain.StateStopped {
		t.Fatal("frozen agent still running")
	}
}

// Rules H4, PROTOCOL.md handshake step 2 and S2: the welcome carries identity, tokens, gateway and summary.
func TestHello_WelcomeCarriesIdentityTokensGatewayAndSummary(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	at := time.Date(2026, 11, 19, 19, 0, 0, 0, time.UTC)
	_ = w.summaries.Save(t.Context(), "mira", app.Summary{Text: "Chatted with alice.", CompactedAt: at, PausedTurnID: "trn_01JD8X3Q5Y6Z7A8B9C0D1E2F3H"})
	_ = w.sup.Wake(t.Context(), "mira", core.WakeChat, json.RawMessage(`{}`))

	_, _, res := w.connect(t, "mira")

	mcp := res.Mcp.(protocol.McpEndpointHttp)
	switch {
	case res.ProtocolVersion != "1.0", res.Agent.Name != "mira", res.Agent.Uid != 200001, res.Agent.Manager == nil || *res.Agent.Manager != "founder":
		t.Errorf("agent = %+v", res.Agent)
	case mcp.Url != "http://127.0.0.1:7700/mcp" || mcp.Token == "":
		t.Errorf("mcp = %+v", mcp)
	case res.Model.BaseUrl != "http://gateway.agentsone.internal:4000" || res.Model.Name != "autoagents/one" || res.Model.Token != "fake-gateway-token-mira":
		t.Errorf("model = %+v", res.Model)
	case res.Session.Summary == nil || *res.Session.Summary != "Chatted with alice." || res.Session.CompactedAt == nil:
		t.Errorf("session = %+v", res.Session)
	case res.Session.PausedTurnID == nil || *res.Session.PausedTurnID != "trn_01JD8X3Q5Y6Z7A8B9C0D1E2F3H":
		t.Errorf("paused turn = %v", res.Session.PausedTurnID)
	case res.Config.HeartbeatIntervalMs != 15000 || res.Config.HeartbeatTimeoutMs != 45000 || res.Config.MaxLineBytes != 1048576:
		t.Errorf("config = %+v", res.Config)
	}
	caller, err := w.fakes.Tokens.Verify(t.Context(), string(mcp.Token))
	if err != nil || caller.Name != "mira" || !slices.Equal(caller.Scopes, []string{app.MCPScope}) {
		t.Errorf("mcp token caller = %+v, %v", caller, err)
	}
}

func TestHello_FirstWelcomeHasNoSummary(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	_, _, res := w.connect(t, "founder")
	if res.Session.Summary != nil || res.Session.PausedTurnID != nil || res.Agent.Manager != nil {
		t.Fatalf("session = %+v manager = %v", res.Session, res.Agent.Manager)
	}
}

func TestHello_RefusesSecondConnection(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.connect(t, "founder")
	second := w.sup.Connect("founder")
	second.Attach(newFakeConn())
	if _, err := second.Hello(t.Context(), "1.0", helloParams); !errors.Is(err, domain.ErrNotPermitted) {
		t.Fatalf("second Hello() error = %v, want ErrNotPermitted", err)
	}
}

func startTurn(t *testing.T, w *world, agent string) (*app.Handler, *fakeConn, common.TurnID) {
	t.Helper()
	_ = w.sup.Wake(t.Context(), agent, core.WakeChat, json.RawMessage(`{}`))
	h, conn, _ := w.connect(t, agent)
	var p struct {
		TurnID common.TurnID `json:"turnId"`
	}
	_ = json.Unmarshal(conn.next(t).params, &p)
	if err := h.Status(t.Context(), protocol.StatusParams{State: "working", TurnID: &p.TurnID}); err != nil {
		t.Fatal(err)
	}
	return h, conn, p.TurnID
}

// Rule P1: profile.update goes to the live list; a turn without one is a fault.
func TestTurn_ProfileUpdateGoesToLiveList(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	h, _, turn := startTurn(t, w, "mira")

	if err := h.ProfileUpdate(t.Context(), protocol.ProfileUpdateParams{TurnID: turn, Line: "Goal: help. Now: answering alice."}); err != nil {
		t.Fatal(err)
	}
	_ = h.Status(t.Context(), protocol.StatusParams{State: "idle", TurnID: &turn})

	lines, _ := w.fakes.Live.Lines(t.Context())
	if len(lines) != 1 || lines[0].Agent != "mira" || lines[0].Line != "Goal: help. Now: answering alice." {
		t.Fatalf("live lines = %+v", lines)
	}
	for _, a := range w.actions() {
		if strings.HasPrefix(a, app.ActionTurnFault) {
			t.Fatalf("fault recorded for a turn with profile.update: %v", w.actions())
		}
	}
}

func TestTurn_EndingWithoutProfileIsRecordedAsFault(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	h, _, turn := startTurn(t, w, "mira")

	_ = h.Status(t.Context(), protocol.StatusParams{State: "idle", TurnID: &turn})

	if !slices.Contains(w.actions(), "agent.turn.fault agent:mira") {
		t.Fatalf("audit = %v, want agent.turn.fault", w.actions())
	}
}

// Rule T6: output, usage, bus.send and task.done must name the running turn.
func TestTurn_RejectsMessagesForAnotherTurn(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	h, _, _ := startTurn(t, w, "mira")
	other := common.TurnID("trn_01JD8X3Q5Y6Z7A8B9C0D1E2F99")
	ctx := t.Context()
	errs := map[string]error{
		"output":         h.Output(ctx, protocol.OutputParams{TurnID: other, Text: "x", Final: true}),
		"usage":          h.Usage(ctx, protocol.UsageParams{TurnID: other}),
		"profile.update": h.ProfileUpdate(ctx, protocol.ProfileUpdateParams{TurnID: other, Line: "x"}),
		"task.done":      h.TaskDone(ctx, protocol.TaskDoneParams{TurnID: other}),
	}
	_, errs["bus.send"] = h.BusSend(ctx, protocol.BusSendParams{TurnID: other, To: "watcher"})
	for m, err := range errs {
		if !errors.Is(err, domain.ErrUnknownTurn) {
			t.Errorf("%s error = %v, want ErrUnknownTurn", m, err)
		}
	}
	if len(w.sink.chunks) != 0 || len(w.sink.usage) != 0 || len(w.fakes.Bus.Sent()) != 0 {
		t.Error("a message for another turn reached a port")
	}
}

func TestTurn_ForwardsOutputAndUsage(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	h, _, turn := startTurn(t, w, "mira")
	chat := common.ChatID("cht_01JD8X3Q5Y6Z7A8B9C0D1E2F40")

	_ = h.Output(t.Context(), protocol.OutputParams{TurnID: turn, ChatID: &chat, Chunk: 0, Text: "Hello ", Final: false})
	_ = h.Usage(t.Context(), protocol.UsageParams{TurnID: turn, Model: "echo", Weight: "quick", TokensIn: 12, TokensOut: 4, CacheReadTokens: ptr(3)})

	want := []app.OutputChunk{{TurnID: string(turn), ChatID: string(chat), Index: 0, Text: "Hello "}}
	if diff := cmp.Diff(want, w.sink.chunks); diff != "" {
		t.Errorf("output (-want +got):\n%s", diff)
	}
	if u := w.sink.usage; len(u) != 1 || u[0].TokensIn != 12 || u[0].CacheReadTokens != 3 || u[0].Model != "echo" {
		t.Errorf("usage = %+v", u)
	}
}

func TestBusSend_SendsFromAgentToKnownRecipient(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	h, _, turn := startTurn(t, w, "founder")

	res, err := h.BusSend(t.Context(), protocol.BusSendParams{TurnID: turn, To: "watcher", Subject: "alice said hello", Body: "alice opened a chat."})
	if err != nil {
		t.Fatal(err)
	}
	sent := w.fakes.Bus.Sent()
	if len(sent) != 1 || sent[0].From != "agent:founder" || sent[0].To != "agent:watcher" || string(res.MessageID) != sent[0].ID {
		t.Fatalf("sent = %+v, result = %+v", sent, res)
	}
	if !strings.HasPrefix(sent[0].Body, "alice said hello\n\n") {
		t.Errorf("body = %q, want subject first", sent[0].Body)
	}
}

func TestBusSend_RefusesUnknownRecipient(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	h, _, turn := startTurn(t, w, "founder")
	_, err := h.BusSend(t.Context(), protocol.BusSendParams{TurnID: turn, To: "ghost", Subject: "s", Body: "b"})
	if !errors.Is(err, domain.ErrUnknownRecipient) || len(w.fakes.Bus.Sent()) != 0 {
		t.Fatalf("error = %v sent = %d, want ErrUnknownRecipient and nothing sent", err, len(w.fakes.Bus.Sent()))
	}
}

func TestTaskDone_CompletesOwnTaskOnly(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		assignee string
		taskID   string
		outcome  protocol.TaskDoneParamsOutcome
		wantErr  error
		want     string
	}{
		{"own task done", "mira", "", "done", nil, core.TaskDone},
		{"own task declined", "mira", "", "declined", nil, core.TaskCancelled},
		{"another agent's task", "watcher", "", "done", domain.ErrNotPermitted, ""},
		{"unknown task", "mira", "tsk_01JD8X3Q5Y6Z7A8B9C0D1E2F99", "done", domain.ErrUnknownTask, ""},
		{"unknown outcome", "mira", "", "maybe", core.ErrInvalid, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			w := newWorld(t)
			task, _ := w.fakes.Tasks.CreateTask(t.Context(), core.NewTask{Title: "Write the note", Assignee: tt.assignee, CreatedBy: "human:alice"})
			id := task.ID
			if tt.taskID != "" {
				id = tt.taskID
			}
			h, _, turn := startTurn(t, w, "mira")
			err := h.TaskDone(t.Context(), protocol.TaskDoneParams{TurnID: turn, TaskID: common.TaskID(id), Outcome: tt.outcome, Summary: "Note written", Details: ptr(common.Markdown("Saved."))})
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("TaskDone() error = %v, want %v", err, tt.wantErr)
			}
			got := w.fakes.Tasks.Outcome(task.ID)
			if got.Status != tt.want {
				t.Fatalf("task outcome = %+v, want status %q", got, tt.want)
			}
			if tt.want != "" && got.Result != "Note written\n\nSaved." {
				t.Errorf("result = %q", got.Result)
			}
		})
	}
}

// Rules R1 and R2: a budget resume wake reuses the paused turn id.
func TestWake_ResumesPausedTurnOnBudgetRestored(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	h, conn, turn := startTurn(t, w, "founder")
	_ = h.ProfileUpdate(t.Context(), protocol.ProfileUpdateParams{TurnID: turn, Line: "Goal: g. Now: n."})
	_ = h.Status(t.Context(), protocol.StatusParams{State: "idle", PausedTurnID: &turn})

	ev := json.RawMessage(`{"type":"agentsone.agents.budget.restored","data":{"agent":"founder"}}`)
	if err := w.sup.Wake(t.Context(), "founder", core.WakeEvent, ev); err != nil {
		t.Fatal(err)
	}
	var p struct {
		TurnID common.TurnID `json:"turnId"`
	}
	_ = json.Unmarshal(conn.next(t).params, &p)
	if p.TurnID != turn {
		t.Fatalf("resume wake turn = %s, want paused turn %s", p.TurnID, turn)
	}
}

// PROTOCOL.md cancel, sleep and shutdown: sleep compacts, stores the summary and stops the unit.
func TestSleep_StoresSummaryAndStopsRunner(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	_, conn, turn := startTurn(t, w, "founder")

	if err := w.sup.Sleep(t.Context(), "founder", true); err != nil {
		t.Fatal(err)
	}
	cancel, sleep, shutdown := conn.next(t), conn.next(t), conn.next(t)
	if cancel.method != "cancel" || !strings.Contains(string(cancel.params), string(turn)) || sleep.method != "sleep" || shutdown.method != "shutdown" {
		t.Fatalf("requests = %s, %s, %s", cancel.method, sleep.method, shutdown.method)
	}
	if s, _ := w.summaries.Load(t.Context(), "founder"); s.Text != "Chatted with alice." || !s.CompactedAt.Equal(w.fakes.Clock.Now()) {
		t.Fatalf("summary = %+v", s)
	}
	if w.launcher.isRunning("founder") || w.sup.SessionState("founder") != domain.StateStopped {
		t.Fatal("sleeping agent still has a process")
	}
	select {
	case <-conn.closed:
	case <-time.After(5 * time.Second):
		t.Fatal("connection not closed after sleep")
	}

	_ = w.sup.Wake(t.Context(), "founder", core.WakeChat, json.RawMessage(`{}`))
	_, _, res := w.connect(t, "founder")
	if res.Session.Summary == nil || *res.Session.Summary != "Chatted with alice." {
		t.Fatalf("next welcome summary = %v, want the stored summary (S2)", res.Session.Summary)
	}
}

func TestSleep_WithoutCompactOnlyStops(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	_ = w.sup.Wake(t.Context(), "founder", core.WakeChat, json.RawMessage(`{}`))
	_, conn, _ := w.connect(t, "founder")
	conn.next(t)
	if err := w.sup.Sleep(t.Context(), "founder", false); err != nil {
		t.Fatal(err)
	}
	if got := conn.next(t); got.method != "shutdown" {
		t.Fatalf("request = %s, want shutdown only", got.method)
	}
	if s, _ := w.summaries.Load(t.Context(), "founder"); s.Text != "" {
		t.Fatalf("summary stored without compaction: %+v", s)
	}
}

// Rule B2: a silent runner is restarted and -32003 is recorded.
func TestTick_RestartsSilentRunner(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	_ = w.sup.Wake(t.Context(), "founder", core.WakeChat, json.RawMessage(`{}`))
	h, conn, _ := w.connect(t, "founder")
	conn.next(t)

	w.fakes.Clock.Advance(44 * time.Second)
	h.Heard()
	w.fakes.Clock.Advance(44 * time.Second)
	if err := w.sup.Tick(t.Context()); err != nil || w.launcher.restarts["founder"] != 0 {
		t.Fatalf("restarted a runner heard 44s ago: err=%v", err)
	}

	w.fakes.Clock.Advance(time.Second)
	if err := w.sup.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	if w.launcher.restarts["founder"] != 1 || w.sup.SessionState("founder") != domain.StateStarting {
		t.Fatalf("restarts=%d state=%s, want 1 and starting", w.launcher.restarts["founder"], w.sup.SessionState("founder"))
	}
	entries := w.fakes.Audit.Entries()
	last := entries[len(entries)-1]
	if last.Action != app.ActionHeartbeatTimeout || !strings.Contains(string(last.Detail), "-32003") {
		t.Fatalf("audit = %+v, want heartbeat timeout with -32003", last)
	}
	select {
	case <-conn.closed:
	case <-time.After(5 * time.Second):
		t.Fatal("silent runner's connection not closed")
	}
	h.Closed(nil) // the old connection ending must not stop the restarted session
	if w.sup.SessionState("founder") != domain.StateStarting {
		t.Fatalf("state after old connection closed = %s", w.sup.SessionState("founder"))
	}
}

func TestClosed_StopsSessionOfLiveConnection(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	h, _, _ := w.connect(t, "founder")
	h.Closed(errors.New("reset"))
	if w.sup.SessionState("founder") != domain.StateStopped {
		t.Fatalf("state = %s, want stopped", w.sup.SessionState("founder"))
	}
}

func TestRetire_StopsRemovesAndLocksCustomAgent(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	mira, _ := w.fakes.Agents.GetAgent(t.Context(), "mira")
	_, _ = w.sup.Provision(t.Context(), mira, nil)
	_ = w.sup.Wake(t.Context(), "mira", core.WakeChat, nil)

	if err := w.sup.Retire(t.Context(), "mira"); err != nil {
		t.Fatal(err)
	}
	if _, ok := w.launcher.unit("mira"); ok || w.launcher.isRunning("mira") || !w.accounts.locked["mira"] {
		t.Fatalf("after retire: unit installed=%v running=%v locked=%v", ok, w.launcher.isRunning("mira"), w.accounts.locked["mira"])
	}
	if !slices.Contains(w.actions(), "agent.runner.retire agent:mira") {
		t.Errorf("audit = %v", w.actions())
	}
}

func TestRetire_RootAgentHasNoAccountToLock(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	if err := w.sup.Retire(t.Context(), "founder"); err != nil {
		t.Fatal(err)
	}
	if len(w.accounts.locked) != 0 {
		t.Fatalf("locked = %v, want none for a root agent", w.accounts.locked)
	}
	if err := w.sup.Retire(t.Context(), "ghost"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("retire unknown error = %v, want ErrNotFound", err)
	}
	if err := w.sup.Retire(t.Context(), "Bad"); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("retire bad name error = %v, want ErrInvalid", err)
	}
}

func TestReconfigure_RestartsRunningRunner(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	mira, _ := w.fakes.Agents.GetAgent(t.Context(), "mira")
	_ = w.sup.Wake(t.Context(), "mira", core.WakeChat, nil)
	w.connect(t, "mira")

	limits := domain.Limits{CPUQuotaPercent: 10}
	if err := w.sup.Reconfigure(t.Context(), mira, &limits); err != nil {
		t.Fatal(err)
	}
	if u, _ := w.launcher.unit("mira"); u.Limits != limits || w.launcher.restarts["mira"] != 1 {
		t.Fatalf("unit limits = %+v restarts = %d", u.Limits, w.launcher.restarts["mira"])
	}
}

func TestReconfigure_LeavesStoppedRunnerStopped(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	mira, _ := w.fakes.Agents.GetAgent(t.Context(), "mira")
	if err := w.sup.Reconfigure(t.Context(), mira, nil); err != nil {
		t.Fatal(err)
	}
	if w.launcher.restarts["mira"] != 0 || w.launcher.isRunning("mira") {
		t.Fatal("stopped runner was started by reconfigure")
	}
}

func TestStatus_ErrorStateIsAudited(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	h, _, _ := startTurn(t, w, "founder")
	h.Error(t.Context(), protocol.ErrorParams{Code: 1020, Message: "Model unavailable", Fatal: true})
	h.Error(t.Context(), protocol.ErrorParams{Code: 1010, Message: "Budget exhausted", Fatal: false})
	h.Log(t.Context(), protocol.LogParams{Level: "info", Message: "hello"})
	_ = h.Status(t.Context(), protocol.StatusParams{State: "error"})
	got := 0
	for _, a := range w.actions() {
		if a == "agent.runner.error agent:founder" {
			got++
		}
	}
	if got != 2 || w.sup.SessionState("founder") != domain.StateFailed {
		t.Fatalf("runner.error records = %d state = %s, want 2 (fatal error + error state) and failed", got, w.sup.SessionState("founder"))
	}
}
