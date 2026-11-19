// SPDX-License-Identifier: MIT

package domain_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/agents/supervisor/domain"
)

var t0 = time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)

func TestValidateName_AcceptsOnlyContractAgentNames(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   string
		ok   bool
	}{
		{"one letter", "a", true},
		{"letters digits hyphen", "mira-2", true},
		{"31 characters", "a" + strings.Repeat("b", 30), true},
		{"32 characters", "a" + strings.Repeat("b", 31), false},
		{"empty", "", false},
		{"digit first", "2mira", false},
		{"upper case", "Mira", false},
		{"slash for path traversal", "../root", false},
		{"dot", "mira.x", false},
		{"space", "mi ra", false},
		{"hyphen first", "-mira", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := domain.ValidateName(tt.in)
			if tt.ok != (err == nil) {
				t.Fatalf("ValidateName(%q) error = %v, want ok=%v", tt.in, err, tt.ok)
			}
			if err != nil && !errors.Is(err, domain.ErrBadName) {
				t.Fatalf("error = %v, want ErrBadName", err)
			}
		})
	}
}

// PROTOCOL.md rule H4: the highest version both sides speak.
func TestNegotiate_PicksHighestCommonVersion(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name           string
		runner, daemon []string
		want           string
		wantErr        error
	}{
		{"single match", []string{"1.0"}, []string{"1.0"}, "1.0", nil},
		{"highest common", []string{"2.1", "2.0", "1.0"}, []string{"2.0", "1.0"}, "2.0", nil},
		{"numeric not string order", []string{"1.9", "1.10"}, []string{"1.9", "1.10"}, "1.10", nil},
		{"runner lists low first", []string{"1.0", "2.0"}, []string{"1.0", "2.0"}, "2.0", nil},
		{"malformed ignored", []string{"x", "1", "1.y", "-1.0", "1.0"}, []string{"x", "1", "1.y", "-1.0", "1.0"}, "1.0", nil},
		{"no common version (H5)", []string{"3.0"}, []string{"1.0"}, "", domain.ErrNoCommonVersion},
		{"empty runner list", nil, []string{"1.0"}, "", domain.ErrNoCommonVersion},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := domain.Negotiate(tt.runner, tt.daemon)
			if !errors.Is(err, tt.wantErr) || got != tt.want {
				t.Fatalf("Negotiate() = %q, %v, want %q, %v", got, err, tt.want, tt.wantErr)
			}
		})
	}
}

func welcomed(t *testing.T) *domain.Session {
	t.Helper()
	s := domain.NewSession("founder")
	if err := s.Start(t0); err != nil {
		t.Fatal(err)
	}
	if err := s.Welcome(t0); err != nil {
		t.Fatal(err)
	}
	return s
}

func idle(t *testing.T) *domain.Session {
	t.Helper()
	s := welcomed(t)
	if _, err := s.Status(domain.RunnerStatus{State: "idle"}); err != nil {
		t.Fatal(err)
	}
	return s
}

func working(t *testing.T, turn string) *domain.Session {
	t.Helper()
	s := idle(t)
	if _, err := s.Status(domain.RunnerStatus{State: "working", TurnID: turn}); err != nil {
		t.Fatal(err)
	}
	return s
}

// Rule H8: no request before the first status idle.
func TestSession_IsReadyOnlyAfterFirstIdleStatus(t *testing.T) {
	t.Parallel()
	s := domain.NewSession("founder")
	if s.Ready() || s.Agent() != "founder" || s.State() != domain.StateStopped {
		t.Fatalf("new session ready=%v state=%s", s.Ready(), s.State())
	}
	_ = s.Start(t0)
	_ = s.Welcome(t0)
	if s.Ready() {
		t.Fatal("ready right after welcome, want not ready until status idle (H8)")
	}
	if _, err := s.Status(domain.RunnerStatus{State: "idle"}); err != nil {
		t.Fatal(err)
	}
	if !s.Ready() {
		t.Fatal("not ready after first status idle")
	}
}

// Rule H7: the first status after the welcome is idle.
func TestSession_RejectsFirstStatusOtherThanIdle(t *testing.T) {
	t.Parallel()
	s := welcomed(t)
	_, err := s.Status(domain.RunnerStatus{State: "working", TurnID: "trn_1"})
	if !errors.Is(err, domain.ErrOutOfOrder) {
		t.Fatalf("error = %v, want ErrOutOfOrder", err)
	}
}

func TestSession_StartAndWelcomeFollowTheLifecycle(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		do   func(s *domain.Session) error
	}{
		{"start twice", func(s *domain.Session) error { _ = s.Start(t0); return s.Start(t0) }},
		{"welcome when stopped", func(s *domain.Session) error { return s.Welcome(t0) }},
		{"welcome twice", func(s *domain.Session) error { _ = s.Start(t0); _ = s.Welcome(t0); return s.Welcome(t0) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if err := tt.do(domain.NewSession("a")); !errors.Is(err, domain.ErrOutOfOrder) {
				t.Fatalf("error = %v, want ErrOutOfOrder", err)
			}
		})
	}
	t.Run("start again after failure", func(t *testing.T) {
		t.Parallel()
		s := idle(t)
		s.Fail()
		if err := s.Start(t0); err != nil {
			t.Fatalf("Start after Fail error = %v", err)
		}
	})
}

// Rule P1 and PROTOCOL.md turns step 4: a turn without profile.update is a fault.
func TestSession_FlagsTurnThatEndsWithoutProfileUpdate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		profile bool
		want    bool
	}{
		{"with profile.update", true, false},
		{"without profile.update", false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := working(t, "trn_1")
			if tt.profile {
				if err := s.Profile("trn_1"); err != nil {
					t.Fatal(err)
				}
			}
			out, err := s.Status(domain.RunnerStatus{State: "idle", TurnID: "trn_1"})
			if err != nil {
				t.Fatal(err)
			}
			if out.EndedTurn != "trn_1" || out.MissingProfile != tt.want {
				t.Fatalf("outcome = %+v, want ended trn_1 missing=%v", out, tt.want)
			}
		})
	}
}

// Rule T6: messages name the running turn.
func TestSession_RejectsProfileForAnotherTurn(t *testing.T) {
	t.Parallel()
	s := working(t, "trn_1")
	if err := s.Profile("trn_2"); !errors.Is(err, domain.ErrUnknownTurn) {
		t.Fatalf("error = %v, want ErrUnknownTurn", err)
	}
	if !s.InTurn("trn_1") || s.InTurn("trn_2") || s.Turn() != "trn_1" {
		t.Fatalf("InTurn mismatch, turn=%s", s.Turn())
	}
}

// Rules R1 and R2: a paused turn is not a fault, and resuming it needs no new profile.update.
func TestSession_ResumesPausedTurnWithoutNewProfile(t *testing.T) {
	t.Parallel()
	s := working(t, "trn_1")
	_ = s.Profile("trn_1")
	out, err := s.Status(domain.RunnerStatus{State: "idle", PausedTurnID: "trn_1"})
	if err != nil {
		t.Fatal(err)
	}
	if out.PausedTurn != "trn_1" || out.MissingProfile || s.PausedTurn() != "trn_1" {
		t.Fatalf("pause outcome = %+v paused=%s", out, s.PausedTurn())
	}
	if _, err := s.Status(domain.RunnerStatus{State: "working", TurnID: "trn_1"}); err != nil {
		t.Fatal(err)
	}
	out, _ = s.Status(domain.RunnerStatus{State: "idle", TurnID: "trn_1"})
	if out.MissingProfile || s.PausedTurn() != "" {
		t.Fatalf("resumed turn outcome = %+v, want no fault and no paused turn", out)
	}
}

func TestSession_PausedTurnSurvivesStop(t *testing.T) {
	t.Parallel()
	s := working(t, "trn_1")
	_, _ = s.Status(domain.RunnerStatus{State: "idle", PausedTurnID: "trn_1"})
	s.Stop()
	if s.PausedTurn() != "trn_1" || s.State() != domain.StateStopped {
		t.Fatalf("after stop paused=%q state=%s", s.PausedTurn(), s.State())
	}
}

func TestSession_StatusTransitions(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		setup   func(t *testing.T) *domain.Session
		status  domain.RunnerStatus
		want    domain.State
		wantErr error
	}{
		{"idle to sleeping", idle, domain.RunnerStatus{State: "sleeping"}, domain.StateSleeping, nil},
		{"sleeping during a turn", func(t *testing.T) *domain.Session { return working(t, "trn_1") }, domain.RunnerStatus{State: "sleeping"}, domain.StateWorking, domain.ErrOutOfOrder},
		{"error fails the session", func(t *testing.T) *domain.Session { return working(t, "trn_1") }, domain.RunnerStatus{State: "error"}, domain.StateFailed, nil},
		{"working twice", func(t *testing.T) *domain.Session { return working(t, "trn_1") }, domain.RunnerStatus{State: "working", TurnID: "trn_2"}, domain.StateWorking, domain.ErrOutOfOrder},
		{"working without turn", idle, domain.RunnerStatus{State: "working"}, domain.StateIdle, domain.ErrOutOfOrder},
		{"unknown state", idle, domain.RunnerStatus{State: "dancing"}, domain.StateIdle, domain.ErrOutOfOrder},
		{"idle while idle", idle, domain.RunnerStatus{State: "idle"}, domain.StateIdle, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := tt.setup(t)
			_, err := s.Status(tt.status)
			if !errors.Is(err, tt.wantErr) || s.State() != tt.want {
				t.Fatalf("state = %s, err = %v; want %s, %v", s.State(), err, tt.want, tt.wantErr)
			}
		})
	}
}

func TestSession_ErrorStatusEndsRunningTurn(t *testing.T) {
	t.Parallel()
	s := working(t, "trn_1")
	out, _ := s.Status(domain.RunnerStatus{State: "error"})
	if !out.Failed || out.EndedTurn != "trn_1" {
		t.Fatalf("outcome = %+v, want failed, ended trn_1", out)
	}
}

// Rule B2: silent for heartbeatTimeoutMs means expired; the edge counts.
func TestSession_ExpiresAfterHeartbeatTimeout(t *testing.T) {
	t.Parallel()
	const timeout = 45 * time.Second
	tests := []struct {
		name  string
		after time.Duration
		want  bool
	}{
		{"just inside", timeout - time.Millisecond, false},
		{"on the limit", timeout, true},
		{"just outside", timeout + time.Millisecond, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := idle(t)
			if got := s.Expired(t0.Add(tt.after), timeout); got != tt.want {
				t.Fatalf("Expired(+%v) = %v, want %v", tt.after, got, tt.want)
			}
		})
	}
	t.Run("any line resets the deadline", func(t *testing.T) {
		t.Parallel()
		s := idle(t)
		s.Heard(t0.Add(30 * time.Second))
		s.Heard(t0) // an older time never moves the deadline back
		if s.Expired(t0.Add(timeout), timeout) {
			t.Fatal("expired 45s after start although a line came at 30s")
		}
	})
	t.Run("a stopped runner never expires", func(t *testing.T) {
		t.Parallel()
		s := domain.NewSession("a")
		if s.Expired(t0.Add(time.Hour), timeout) {
			t.Fatal("stopped session expired")
		}
	})
}

func TestPlanWake_ActionPerState(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		frozen bool
		state  domain.State
		want   domain.WakeAction
	}{
		{"frozen idle refused", true, domain.StateIdle, domain.WakeRefuse},
		{"frozen stopped refused", true, domain.StateStopped, domain.WakeRefuse},
		{"stopped starts", false, domain.StateStopped, domain.WakeStart},
		{"sleeping starts", false, domain.StateSleeping, domain.WakeStart},
		{"failed starts", false, domain.StateFailed, domain.WakeStart},
		{"starting holds", false, domain.StateStarting, domain.WakeHold},
		{"welcomed holds", false, domain.StateWelcomed, domain.WakeHold},
		{"idle sends", false, domain.StateIdle, domain.WakeSend},
		{"working sends (runner queues, T7)", false, domain.StateWorking, domain.WakeSend},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := domain.PlanWake(tt.frozen, tt.state); got != tt.want {
				t.Fatalf("PlanWake(%v, %s) = %v, want %v", tt.frozen, tt.state, got, tt.want)
			}
		})
	}
}

func TestAccountFor_RootAndCustom(t *testing.T) {
	t.Parallel()
	root := domain.AccountFor("founder", true)
	if root.User != "root" || root.Create || !root.IsRoot || root.Home != "/root" {
		t.Fatalf("root account = %+v", root)
	}
	c := domain.AccountFor("mira", false)
	want := domain.Account{User: "mira", Group: "mira", Home: "/home/agents/mira", Shell: "/usr/sbin/nologin", Create: true}
	if c != want {
		t.Fatalf("custom account = %+v, want %+v", c, want)
	}
}

func TestAccount_WithUIDPinsOnlyPositiveUIDsOfCustomAgents(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		isRoot bool
		uid    int
		want   int
	}{
		{"custom with known uid", false, 1001, 1001},
		{"custom with zero allocates", false, 0, 0},
		{"custom with -1 allocates", false, -1, 0},
		{"root ignores uid", true, 1001, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := domain.AccountFor("mira", tt.isRoot).WithUID(tt.uid).UID; got != tt.want {
				t.Fatalf("UID = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestUnitSpec_AcceptsPlainSpec(t *testing.T) {
	t.Parallel()
	spec := domain.UnitSpec{
		Agent:   "mira",
		Account: domain.AccountFor("mira", false),
		Exec:    domain.Exec{Path: "/usr/lib/agentsone/runners/echo", Args: []string{"--name", `a "b" $HOME 50%`}, Env: []string{"ECHO_MODE=fast"}, Dir: "/home/agents/mira"},
		Limits:  domain.Limits{CPUQuotaPercent: 50, MemoryMaxBytes: 1 << 30, TasksMax: 64},
	}
	if err := spec.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestUnitSpec_RefusesInjection(t *testing.T) {
	t.Parallel()
	base := func() domain.UnitSpec {
		return domain.UnitSpec{Agent: "mira", Account: domain.AccountFor("mira", false), Exec: domain.Exec{Path: "/bin/r"}}
	}
	tests := []struct {
		name    string
		mutate  func(*domain.UnitSpec)
		wantErr error
	}{
		{"newline in argument", func(u *domain.UnitSpec) { u.Exec.Args = []string{"x\nUser=root"} }, domain.ErrUnsafeValue},
		{"newline in env", func(u *domain.UnitSpec) { u.Exec.Env = []string{"A=1\nUser=root"} }, domain.ErrUnsafeValue},
		{"env without name", func(u *domain.UnitSpec) { u.Exec.Env = []string{"=1"} }, domain.ErrUnsafeValue},
		{"env without equals", func(u *domain.UnitSpec) { u.Exec.Env = []string{"A"} }, domain.ErrUnsafeValue},
		{"relative exec path", func(u *domain.UnitSpec) { u.Exec.Path = "bin/r" }, domain.ErrUnsafeValue},
		{"nul in path", func(u *domain.UnitSpec) { u.Exec.Path = "/bin/r\x00" }, domain.ErrUnsafeValue},
		{"newline in dir", func(u *domain.UnitSpec) { u.Exec.Dir = "/tmp\nUser=root" }, domain.ErrUnsafeValue},
		{"bad agent name", func(u *domain.UnitSpec) { u.Agent = "../x" }, domain.ErrBadName},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			u := base()
			tt.mutate(&u)
			if err := u.Validate(); !errors.Is(err, tt.wantErr) {
				t.Fatalf("Validate() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestSocketPath_FollowsTheProtocolConvention(t *testing.T) {
	t.Parallel()
	if got := domain.SocketPath("mira"); got != "/run/agentsone/runners/mira.sock" {
		t.Errorf("SocketPath = %q", got)
	}
}
