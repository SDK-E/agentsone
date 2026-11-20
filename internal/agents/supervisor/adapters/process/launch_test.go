// SPDX-License-Identifier: MIT

package process

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// fakeLaunch records the helper's steps. fail names a step that fails.
type fakeLaunch struct {
	steps []string
	fail  string
}

func (f *fakeLaunch) step(s, kind string) error {
	f.steps = append(f.steps, s)
	if f.fail == kind {
		return errors.New("refused")
	}
	return nil
}

func (f *fakeLaunch) JoinCgroup(dir string) error { return f.step("join "+dir, "join") }
func (f *fakeLaunch) LowerLimit(k rlimitKind, v uint64) error {
	return f.step(fmt.Sprintf("limit %s %d", k, v), "limit "+k.String())
}
func (f *fakeLaunch) SetIDs(uid, gid int) error {
	return f.step(fmt.Sprintf("ids %d %d", uid, gid), "ids")
}
func (f *fakeLaunch) Exec(path string, argv []string) error {
	return f.step("exec "+path+" "+strings.Join(argv, " "), "exec")
}

var fullPlan = launchPlan{Cgroup: "/sys/fs/cgroup/agents/mira", UID: 200001, GID: 200001,
	NoFile: 4096, NProc: 512, Path: "/usr/bin/runner", Args: []string{"--serve", "-v"}}

func TestLaunch_ArgumentsRoundTrip(t *testing.T) {
	t.Parallel()
	for _, p := range []launchPlan{fullPlan, {UID: -1, GID: -1, Path: "/bin/sh"}} {
		got, err := parseLaunch(p.args())
		if err != nil {
			t.Fatalf("parseLaunch(%v) error = %v", p.args(), err)
		}
		if p.Args == nil {
			p.Args = []string{}
		}
		if diff := cmp.Diff(p, got); diff != "" {
			t.Errorf("round trip (-want +got):\n%s", diff)
		}
	}
}

func TestLaunch_RefusesBadArguments(t *testing.T) {
	t.Parallel()
	good := fullPlan.args()
	tests := map[string][]string{
		"option without value": {"--cgroup"},
		"unknown option":       append([]string{"--chroot", "/"}, good...),
		"bad number":           {"--uid", "x"},
		"missing option":       good[2:],
		"no runner":            good[:len(good)-3],
		"empty runner":         append(append([]string{}, good[:len(good)-3]...), ""),
		"uid without gid":      launchPlan{UID: 5, GID: -1, Path: "/r"}.args(),
		"uid 0":                launchPlan{UID: 0, GID: 0, Path: "/r"}.args(),
	}
	for name, args := range tests {
		if _, err := parseLaunch(args); !errors.Is(err, errBadLaunch) {
			t.Errorf("%s: parseLaunch(%v) error = %v, want errBadLaunch", name, args, err)
		}
	}
}

// The runner is in its cgroup and limited before it drops privileges, and
// it drops them before it runs.
func TestLaunch_JoinsCgroupLimitsDropsThenExecs(t *testing.T) {
	t.Parallel()
	f := &fakeLaunch{}
	if err := runLaunch(fullPlan, f, io.Discard); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"join /sys/fs/cgroup/agents/mira",
		"limit core 0", "limit nofile 4096", "limit nproc 512",
		"ids 200001 200001",
		"exec /usr/bin/runner /usr/bin/runner --serve -v",
	}
	if diff := cmp.Diff(want, f.steps); diff != "" {
		t.Fatalf("steps (-want +got):\n%s", diff)
	}
}

// The helper says it is ready right before the exec, and only then, so
// the daemon can tell a started runner from a helper that died first.
func TestLaunch_ReportsReadyRightBeforeExec(t *testing.T) {
	t.Parallel()
	var report strings.Builder
	f := &readyCheck{report: &report}
	if err := runLaunch(fullPlan, f, &report); err != nil {
		t.Fatal(err)
	}
	if f.atJoin != "" || f.atExec != launchReady {
		t.Fatalf("report at join %q, at exec %q; want empty, then %q", f.atJoin, f.atExec, launchReady)
	}
}

// readyCheck is a fakeLaunch that notes the report at the first and the
// last step.
type readyCheck struct {
	fakeLaunch
	report         *strings.Builder
	atJoin, atExec string
}

func (r *readyCheck) JoinCgroup(dir string) error {
	r.atJoin = r.report.String()
	return r.fakeLaunch.JoinCgroup(dir)
}

func (r *readyCheck) Exec(path string, argv []string) error {
	r.atExec = r.report.String()
	return r.fakeLaunch.Exec(path, argv)
}

func TestLaunch_SkipsWhatThePlanLeavesOut(t *testing.T) {
	t.Parallel()
	f := &fakeLaunch{}
	if err := runLaunch(launchPlan{UID: -1, GID: -1, Path: "/bin/sh"}, f, io.Discard); err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff([]string{"limit core 0", "exec /bin/sh /bin/sh"}, f.steps); diff != "" {
		t.Fatalf("steps (-want +got):\n%s", diff)
	}
}

// A failed step stops the helper: the runner never starts half set up.
func TestLaunch_StopsAtTheFirstFailedStep(t *testing.T) {
	t.Parallel()
	for _, fail := range []string{"join", "limit core", "limit nproc", "ids", "exec"} {
		f := &fakeLaunch{fail: fail}
		var report strings.Builder
		err := runLaunch(fullPlan, f, &report)
		if err == nil {
			t.Fatalf("%s fails: runLaunch error = nil", fail)
		}
		if ready := report.String() == launchReady; ready != (fail == "exec") {
			t.Errorf("%s fails: report %q, want ready only when exec itself fails", fail, report.String())
		}
		last := f.steps[len(f.steps)-1]
		if !strings.HasPrefix(last, strings.Split(fail, " ")[0]) {
			t.Errorf("%s fails: last step = %q, want it to stop there", fail, last)
		}
		if fail != "exec" && strings.HasPrefix(last, "exec") {
			t.Errorf("%s fails: runner was exec'd", fail)
		}
	}
}

func TestLaunchMain_ReportsWhyItFailed(t *testing.T) {
	t.Parallel()
	var report strings.Builder
	if code := launchMain([]string{"--uid"}, &fakeLaunch{}, &report); code != LaunchFailed {
		t.Errorf("bad args exit = %d, want %d", code, LaunchFailed)
	}
	if !strings.Contains(report.String(), "--uid has no value") {
		t.Errorf("report = %q", report.String())
	}

	report.Reset()
	f := &fakeLaunch{fail: "join"}
	if code := launchMain(fullPlan.args(), f, &report); code != LaunchFailed || !strings.Contains(report.String(), "join cgroup") {
		t.Errorf("join fails: exit %d report %q", code, report.String())
	}

	// A fake exec returns; the real one does not unless it fails.
	report.Reset()
	if code := launchMain(fullPlan.args(), &fakeLaunch{}, &report); code != LaunchFailed || report.Len() == 0 {
		t.Errorf("exec returned: exit %d report %q", code, report.String())
	}
}

func TestLowered_NeverRaisesAboveTheHardLimit(t *testing.T) {
	t.Parallel()
	if lowered(4096, 1024) != 1024 || lowered(512, 1024) != 512 {
		t.Fatal("lowered must cap at the hard limit")
	}
}
