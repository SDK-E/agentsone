// SPDX-License-Identifier: MIT

//go:build linux

package process

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SDK-E/agentsone/internal/agents/supervisor/domain"
	"github.com/SDK-E/agentsone/internal/core"
)

// pipeState is a sysState whose helper wrote msg to its report pipe and
// then ended, as afterStart sees it.
func pipeState(t *testing.T, msg string) *sysState {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.WriteString(msg); err != nil {
		t.Fatal(err)
	}
	return &sysState{mode: domain.LimitsSoft, report: r, write: w}
}

// A helper that died before it exec'd the runner, for example when its own
// runtime could not start a thread, wrote nothing. That is a failed start,
// never a running runner.
func TestAfterStart_HelperThatDiedBeforeExecIsAStartFailure(t *testing.T) {
	t.Parallel()
	st := pipeState(t, "")
	if mode, err := st.afterStart(); !errors.Is(err, errLaunch) {
		t.Fatalf("afterStart() = %q, %v; want %v", mode, err, errLaunch)
	}
}

func TestAfterStart_ReadsTheHelpersReport(t *testing.T) {
	t.Parallel()
	if mode, err := pipeState(t, launchReady).afterStart(); err != nil || mode != domain.LimitsSoft {
		t.Fatalf("ready: afterStart() = %q, %v; want soft, nil", mode, err)
	}
	_, err := pipeState(t, launchReady+"exec /x: no such file").afterStart()
	if !errors.Is(err, errLaunch) || !strings.HasSuffix(err.Error(), ": exec /x: no such file") {
		t.Fatalf("exec failed: afterStart() error = %v", err)
	}
	_, err = pipeState(t, "join cgroup /c: permission denied").afterStart()
	if !errors.Is(err, errLaunch) || !strings.Contains(err.Error(), "join cgroup") {
		t.Fatalf("join failed: afterStart() error = %v", err)
	}
}

func customSpec(name string, l domain.Limits) domain.UnitSpec {
	return domain.UnitSpec{Agent: name, Account: domain.AccountFor(name, false), Limits: l,
		Exec: domain.Exec{Path: "/bin/true"}}
}

func TestAgentCell_IsTheRunnersCellOrNothing(t *testing.T) {
	t.Parallel()
	limits := domain.Limits{CPUQuotaPercent: 50, MemoryMaxBytes: 1 << 30, TasksMax: 48}
	root := t.TempDir()
	rootAgent := domain.UnitSpec{Agent: "founder", Account: domain.AccountFor("founder", true), Exec: domain.Exec{Path: "/bin/true"}}
	tests := []struct {
		name string
		root string
		spec domain.UnitSpec
		want core.ProcessCell
	}{
		{"custom agent, hard", root, customSpec("mira", limits), core.ProcessCell{Cgroup: filepath.Join(root, "mira"), NoFile: DefaultNoFile}},
		{"custom agent, soft", "", customSpec("mira", limits), core.ProcessCell{NoFile: DefaultNoFile, NProc: 48, Data: 1 << 30}},
		{"root agent, hard", root, rootAgent, core.ProcessCell{Cgroup: filepath.Join(root, "founder")}},
		{"root agent, soft", "", rootAgent, core.ProcessCell{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			l := &Launcher{CgroupRoot: tc.root}
			if err := l.Install(context.Background(), tc.spec); err != nil {
				t.Fatal(err)
			}
			got, err := l.AgentCell(context.Background(), tc.spec.Agent)
			if err != nil || got != tc.want {
				t.Fatalf("AgentCell = %+v, %v; want %+v", got, err, tc.want)
			}
		})
	}
	if b, err := os.ReadFile(filepath.Join(root, "mira", "pids.max")); err != nil || string(b) != "48" { // #nosec G304 -- the test's own temp cgroup root
		t.Errorf("hard cell pids.max = %q, %v; want the runner's 48", b, err)
	}
}

func TestAgentCell_RefusesAnAgentThatWasNeverInstalled(t *testing.T) {
	t.Parallel()
	if _, err := (&Launcher{}).AgentCell(context.Background(), "ghost"); !errors.Is(err, ErrNotInstalled) {
		t.Fatalf("AgentCell(ghost) error = %v, want %v", err, ErrNotInstalled)
	}
}

func TestHumanCell_IsNothingWithoutACgroupRoot(t *testing.T) {
	t.Parallel()
	if c, err := (&Launcher{}).HumanCell(context.Background(), "ada"); err != nil || c.Placed() {
		t.Fatalf("HumanCell = %+v, %v; want the zero cell", c, err)
	}
}

func TestHumanCell_RefusesANameNoHumanCanHave(t *testing.T) {
	t.Parallel()
	l := &Launcher{CgroupRoot: filepath.Join(t.TempDir(), "agents")}
	for _, name := range []string{"", "Ada", "../agents/mira"} {
		if _, err := l.HumanCell(context.Background(), name); err == nil {
			t.Errorf("HumanCell(%q) made a cell", name)
		}
	}
}

// Only root makes cgroups, so a human cell anyone else owns or may write
// is refused: no process in it could be trusted to be the human's.
func TestHumanCell_RefusesACgroupThatIsNotRootsAlone(t *testing.T) {
	t.Parallel()
	fs := t.TempDir()
	if err := os.Mkdir(filepath.Join(fs, HumansCgroup), 0o777); err != nil { // #nosec G301 -- the open folder under test
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(fs, HumansCgroup), 0o777); err != nil { // #nosec G302 -- the open folder under test
		t.Fatal(err)
	}
	l := &Launcher{CgroupRoot: filepath.Join(fs, AgentsCgroup)}
	if _, err := l.HumanCell(context.Background(), "ada"); !errors.Is(err, errNotRootOwned) {
		t.Fatalf("HumanCell in a world-writable folder: %v, want %v", err, errNotRootOwned)
	}
}

func TestHumanCell_MakesTheCellUnderHumans(t *testing.T) {
	t.Parallel()
	fs := t.TempDir()
	l := &Launcher{CgroupRoot: filepath.Join(fs, AgentsCgroup)}
	c, err := l.HumanCell(context.Background(), "ada")
	if os.Geteuid() != 0 {
		// The folders are this user's, not root's.
		if !errors.Is(err, errNotRootOwned) {
			t.Fatalf("HumanCell as uid %d: %v, want %v", os.Geteuid(), err, errNotRootOwned)
		}
		return
	}
	if err != nil || c != (core.ProcessCell{Cgroup: filepath.Join(fs, HumansCgroup, "ada")}) {
		t.Fatalf("HumanCell = %+v, %v", c, err)
	}
}

func TestPlace_RefusesRootOrAHalfUser(t *testing.T) {
	t.Parallel()
	for _, p := range []Placement{{UID: 0, GID: 0}, {UID: 200001, GID: -1}, {UID: -1, GID: 5}} {
		if _, err := Place(exec.Command("/bin/true"), p); !errors.Is(err, errBadLaunch) {
			t.Errorf("Place(%+v) error = %v, want %v", p, err, errBadLaunch)
		}
	}
}

// A placed program starts through the real launch helper (this test
// binary), which lowers its limits and execs it.
func TestPlace_StartsTheProgramThroughTheHelper(t *testing.T) {
	t.Parallel()
	cmd := exec.Command("/bin/sh", "-c", `test "$(ulimit -n)" = 64`)
	p, err := Place(cmd, Placement{Cell: core.ProcessCell{NoFile: 64}, UID: -1, GID: -1})
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Path != selfExe || cmd.Args[0] != LaunchArgv0 {
		t.Fatalf("cmd %s %v, want the launch helper", cmd.Path, cmd.Args)
	}
	if err := cmd.Start(); err != nil {
		p.Close()
		t.Fatal(err)
	}
	if err := p.Started(); err != nil {
		t.Fatalf("Started() = %v", err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("the program saw another open-file limit: %v", err)
	}
}

func TestPlace_ReportsAHelperThatCouldNotPlaceTheProgram(t *testing.T) {
	t.Parallel()
	cmd := exec.Command("/bin/true")
	missing := filepath.Join(t.TempDir(), "no-such-cgroup")
	p, err := Place(cmd, Placement{Cell: core.ProcessCell{Cgroup: missing}, UID: -1, GID: -1})
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		p.Close()
		t.Fatal(err)
	}
	if err := p.Started(); !errors.Is(err, errLaunch) || !strings.Contains(err.Error(), "join cgroup") {
		t.Fatalf("Started() = %v, want the failed join", err)
	}
	var exit *exec.ExitError
	if err := cmd.Wait(); !errors.As(err, &exit) || exit.ExitCode() != LaunchFailed {
		t.Fatalf("helper exit = %v, want %d", err, LaunchFailed)
	}
}

func TestPlaced_CloseWithoutAStart(t *testing.T) {
	t.Parallel()
	p, err := Place(exec.Command("/bin/true"), Placement{UID: -1, GID: -1})
	if err != nil {
		t.Fatal(err)
	}
	p.Close()
	p.Close() // twice is fine
	if p.st.report != nil || p.st.write != nil {
		t.Fatal("report pipe left open")
	}
}
