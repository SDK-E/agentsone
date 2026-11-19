// SPDX-License-Identifier: MIT

package domain_test

import (
	"errors"
	"io/fs"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/machine/domain"
)

var (
	human   = domain.Actor{Kind: domain.ActorHuman, Name: "alice", UID: -1}
	rootAg  = domain.Actor{Kind: domain.ActorAgent, Name: "founder", UID: 0, IsRoot: true}
	custom  = domain.Actor{Kind: domain.ActorAgent, Name: "writer", UID: 200001, Home: "/home/agents/writer"}
	system  = domain.Actor{Kind: domain.ActorSystem, Name: "agentsoned", UID: 0}
	someone = domain.Actor{Kind: domain.ActorAgent, Name: "nohome", UID: 200003}
)

func TestPrivilegedActors(t *testing.T) {
	for _, tc := range []struct {
		a    domain.Actor
		want bool
	}{{human, true}, {rootAg, true}, {system, true}, {custom, false}} {
		if got := tc.a.Privileged(); got != tc.want {
			t.Errorf("%s privileged = %v, want %v", tc.a.Label(), got, tc.want)
		}
	}
	if got := custom.Label(); got != "agent:writer" {
		t.Errorf("label %q", got)
	}
}

func TestRunAsUID(t *testing.T) {
	if human.RunAsUID() != 0 || rootAg.RunAsUID() != 0 || custom.RunAsUID() != 200001 {
		t.Fatalf("run-as uids: %d %d %d", human.RunAsUID(), rootAg.RunAsUID(), custom.RunAsUID())
	}
}

func TestParsePath(t *testing.T) {
	for _, bad := range []string{"", "relative/x", "/a/\x00b"} {
		if _, err := domain.ParsePath(bad); !errors.Is(err, core.ErrInvalid) {
			t.Errorf("ParsePath(%q) err = %v, want ErrInvalid", bad, err)
		}
	}
	p, err := domain.ParsePath("/home/agents/writer/../writer/./notes.txt")
	if err != nil || p != "/home/agents/writer/notes.txt" {
		t.Fatalf("ParsePath = %q, %v", p, err)
	}
}

func TestAreaFor(t *testing.T) {
	for _, tc := range []struct {
		name      string
		a         domain.Actor
		path      string
		wantBase  string
		wantRel   string
		forbidden bool
	}{
		{"root anywhere", rootAg, "/etc/nginx/nginx.conf", "/", "etc/nginx/nginx.conf", false},
		{"human anywhere", human, "/", "/", ".", false},
		{"custom own home", custom, "/home/agents/writer/a.txt", "/home/agents/writer", "a.txt", false},
		{"custom home itself", custom, "/home/agents/writer", "/home/agents/writer", ".", false},
		{"custom shared", custom, "/home/shared/team/x", "/home/shared", "team/x", false},
		{"custom tmp", custom, "/tmp/x", "/tmp", "x", false},
		{"custom other home", custom, "/home/agents/coder/a.txt", "", "", true},
		{"custom prefix trick", custom, "/home/agents/writer2/a.txt", "", "", true},
		{"custom etc", custom, "/etc/passwd", "", "", true},
		{"agent without home", someone, "/home/agents/nohome/x", "", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			area, err := domain.AreaFor(tc.a, tc.path)
			if tc.forbidden {
				if !errors.Is(err, core.ErrForbidden) {
					t.Fatalf("err = %v, want ErrForbidden", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if area.Base != tc.wantBase || area.Rel != tc.wantRel || area.Path != tc.path {
				t.Fatalf("area = %+v", area)
			}
		})
	}
}

func TestParseMode(t *testing.T) {
	m, err := domain.ParseMode("0640")
	if err != nil || m != 0o640 {
		t.Fatalf("ParseMode = %v, %v", m, err)
	}
	for _, bad := range []string{"640", "0999", "abcd", "07777"} {
		if _, err := domain.ParseMode(bad); !errors.Is(err, core.ErrInvalid) {
			t.Errorf("ParseMode(%q) err = %v", bad, err)
		}
	}
	if got := domain.FormatMode(0o755 | fs.ModeDir); got != "0755" {
		t.Errorf("FormatMode = %q", got)
	}
}

func TestCheckFileSize(t *testing.T) {
	if err := domain.CheckFileSize(domain.MaxFileBytes); err != nil {
		t.Fatalf("at cap: %v", err)
	}
	if err := domain.CheckFileSize(domain.MaxFileBytes + 1); !errors.Is(err, domain.ErrTooLarge) {
		t.Fatalf("over cap: %v", err)
	}
}

func TestRecipeWorthy(t *testing.T) {
	for path, want := range map[string]bool{
		"/etc/nginx/nginx.conf":         true,
		"/usr/local/bin/tool":           true,
		"/home/agents/writer/a":         false,
		"/home":                         false,
		"/var/lib/agentsone/core.db":    false,
		"/var/lib/agentsone-edge/x":     true,
		"/tmp/x":                        false,
		"/run/agentsone/api.sock":       false,
		"/proc/1/status":                false,
		"/sys/fs/cgroup/memory.max":     false,
		"/dev/null":                     false,
		"/var/lib/agentsone/recipe/x":   false,
		"/var/lib/agentsoneextra/thing": true,
	} {
		if got := domain.RecipeWorthy(path); got != want {
			t.Errorf("RecipeWorthy(%q) = %v, want %v", path, got, want)
		}
	}
}

func TestParsePackageName(t *testing.T) {
	for _, ok := range []string{"nginx", "libc6", "g++", "python3.13", "lib-x.y+z"} {
		if err := domain.ParsePackageName(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "a", "Nginx", "-x", "nginx; rm -rf /", "nginx=1.0", "x y"} {
		if err := domain.ParsePackageName(bad); !errors.Is(err, core.ErrInvalid) {
			t.Errorf("%q err = %v", bad, err)
		}
	}
}

func TestParsePackageVersion(t *testing.T) {
	for _, ok := range []string{"", "1.0", "1:2.3-4+deb13u1", "2.0~rc1"} {
		if err := domain.ParsePackageVersion(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"1.0 -y", "$(x)", "1.0;"} {
		if err := domain.ParsePackageVersion(bad); !errors.Is(err, core.ErrInvalid) {
			t.Errorf("%q err = %v", bad, err)
		}
	}
}

func TestCanManagePackages(t *testing.T) {
	if err := domain.CanManagePackages(custom); !errors.Is(err, core.ErrForbidden) {
		t.Fatalf("custom: %v", err)
	}
	if err := domain.CanManagePackages(rootAg); err != nil {
		t.Fatalf("root: %v", err)
	}
}

func TestParseSignal(t *testing.T) {
	s, err := domain.ParseSignal("TERM")
	if err != nil || s != domain.SignalTerm {
		t.Fatalf("TERM: %v %v", s, err)
	}
	if _, err := domain.ParseSignal("SEGV"); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("SEGV: %v", err)
	}
	if len(domain.Signals()) != 8 {
		t.Fatalf("signals: %v", domain.Signals())
	}
}

func TestCanSignal(t *testing.T) {
	own := domain.Process{PID: 10, UID: 200001}
	theirs := domain.Process{PID: 11, UID: 200002}
	daemon := domain.Process{PID: 1, UID: 0}
	if err := domain.CanSignal(custom, own); err != nil {
		t.Fatalf("own: %v", err)
	}
	if err := domain.CanSignal(custom, theirs); !errors.Is(err, core.ErrForbidden) {
		t.Fatalf("theirs: %v", err)
	}
	if err := domain.CanSignal(rootAg, theirs); err != nil {
		t.Fatalf("root: %v", err)
	}
	if err := domain.CanSignal(rootAg, daemon); !errors.Is(err, core.ErrForbidden) {
		t.Fatalf("pid 1: %v", err)
	}
}

func TestNewReboot(t *testing.T) {
	now := time.Date(2026, 11, 19, 21, 0, 0, 0, time.UTC)
	r, err := domain.NewReboot(rootAg, "kernel settings", nil, now)
	if err != nil {
		t.Fatal(err)
	}
	want := domain.Reboot{Reason: "kernel settings", RequestedBy: "agent:founder", At: now.Add(10 * time.Second), Delay: 10 * time.Second}
	if diff := cmp.Diff(want, r); diff != "" {
		t.Fatal(diff)
	}
	zero := 0
	if r, _ := domain.NewReboot(human, "now", &zero, now); !r.At.Equal(now) {
		t.Fatalf("zero delay at %v", r.At)
	}
	tooLong := 3601
	for _, tc := range []struct {
		reason string
		delay  *int
	}{{"", nil}, {string(make([]byte, 501)), nil}, {"ok", &tooLong}} {
		if _, err := domain.NewReboot(human, tc.reason, tc.delay, now); !errors.Is(err, core.ErrInvalid) {
			t.Errorf("reason len %d: %v", len(tc.reason), err)
		}
	}
}

func TestRebootEffectIsHonest(t *testing.T) {
	for _, want := range []string{"Restarts the machine container", "recipe", "recreate", "Docker or Coolify"} {
		if !strings.Contains(domain.RebootEffect, want) {
			t.Errorf("reboot effect %q does not mention %q", domain.RebootEffect, want)
		}
	}
}

func TestUserKind(t *testing.T) {
	for uid, want := range map[int]domain.UserKind{0: domain.UserSystem, 33: domain.UserSystem, 200000: domain.UserAgent, 299999: domain.UserAgent, 300000: domain.UserSystem} {
		if got := domain.KindOfUID(uid); got != want {
			t.Errorf("uid %d kind %s, want %s", uid, got, want)
		}
	}
}

func TestPage(t *testing.T) {
	items := []int{1, 2, 3, 4, 5}
	got, next, err := domain.Page(items, "", 2)
	if err != nil || !cmp.Equal(got, []int{1, 2}) || next == "" {
		t.Fatalf("page 1: %v %q %v", got, next, err)
	}
	got, next, err = domain.Page(items, next, 2)
	if err != nil || !cmp.Equal(got, []int{3, 4}) {
		t.Fatalf("page 2: %v %q %v", got, next, err)
	}
	got, next, err = domain.Page(items, next, 2)
	if err != nil || !cmp.Equal(got, []int{5}) || next != "" {
		t.Fatalf("page 3: %v %q %v", got, next, err)
	}
	if _, _, err := domain.Page(items, "zz", 2); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("bad cursor: %v", err)
	}
	if _, _, err := domain.Page(items, "", 0); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("bad limit: %v", err)
	}
	if got, _, _ := domain.Page(items, "", 0+500); len(got) != 5 {
		t.Fatalf("big limit: %v", got)
	}
}

func TestCPUPercent(t *testing.T) {
	prev := domain.CPUSample{BusyMicros: 1_000_000, At: time.Unix(100, 0)}
	cur := domain.CPUSample{BusyMicros: 2_000_000, At: time.Unix(102, 0)}
	if got := domain.CPUPercent(prev, cur, 2); got != 25 {
		t.Fatalf("got %v, want 25", got)
	}
	if got := domain.CPUPercent(cur, cur, 2); got != 0 {
		t.Fatalf("same sample %v", got)
	}
	if got := domain.CPUPercent(prev, domain.CPUSample{BusyMicros: 100_000_000, At: time.Unix(101, 0)}, 1); got != 100 {
		t.Fatalf("clamp %v", got)
	}
}
