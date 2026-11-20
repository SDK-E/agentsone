// SPDX-License-Identifier: MIT

package system_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/machine/adapters/system"
	"github.com/SDK-E/agentsone/internal/machine/app"
	"github.com/SDK-E/agentsone/internal/machine/domain"
)

func ctx() context.Context { return context.Background() }

func write(t *testing.T, p, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

func areaIn(base, rel string) domain.Area {
	return domain.Area{Path: filepath.Join(base, rel), Base: base, Rel: rel}
}

func TestFilesRoundTrip(t *testing.T) {
	base := t.TempDir()
	fsys := system.NewFiles()
	uid, gid := os.Getuid(), os.Getgid()
	e, err := fsys.Write(ctx(), areaIn(base, "etc/app.conf"), app.FileWrite{Data: []byte("port=80"), Mode: 0o640, UID: uid, GID: gid, CreateParents: true})
	if err != nil {
		t.Fatal(err)
	}
	if e.Size != 7 || e.Mode.Perm() != 0o640 || e.Type != domain.FileRegular || e.Name != "app.conf" {
		t.Fatalf("entry %+v", e)
	}
	data, size, err := fsys.Read(ctx(), areaIn(base, "etc/app.conf"), 4)
	if err != nil || string(data) != "port" || size != 7 {
		t.Fatalf("read %q %d %v", data, size, err)
	}
	list, err := fsys.List(ctx(), areaIn(base, "etc"))
	if err != nil || len(list) != 1 || list[0].Path != filepath.Join(base, "etc/app.conf") {
		t.Fatalf("list %+v %v", list, err)
	}
	if err := fsys.Delete(ctx(), areaIn(base, "etc"), false); !errors.Is(err, core.ErrConflict) {
		t.Fatalf("delete non-empty folder: %v", err)
	}
	if err := fsys.Delete(ctx(), areaIn(base, "etc"), true); err != nil {
		t.Fatal(err)
	}
	if _, _, err := fsys.Read(ctx(), areaIn(base, "etc/app.conf"), 4); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("read deleted: %v", err)
	}
}

func TestFilesWriteWithoutParentsIsNotFound(t *testing.T) {
	base := t.TempDir()
	_, err := system.NewFiles().Write(ctx(), areaIn(base, "no/such/x"), app.FileWrite{Data: []byte("x"), Mode: 0o644, UID: os.Getuid(), GID: os.Getgid()})
	if !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestFilesSymlinkCannotLeaveTheArea(t *testing.T) {
	outside := t.TempDir()
	write(t, filepath.Join(outside, "secret"), "shadow")
	home := t.TempDir()
	if err := os.Symlink(filepath.Join(outside, "secret"), filepath.Join(home, "link")); err != nil {
		t.Fatal(err)
	}
	fsys := system.NewFiles()
	if _, _, err := fsys.Read(ctx(), areaIn(home, "link"), 100); !errors.Is(err, core.ErrForbidden) {
		t.Fatalf("read through symlink: %v", err)
	}
	if _, err := fsys.Write(ctx(), areaIn(home, "link"), app.FileWrite{Data: []byte("x"), Mode: 0o644, UID: os.Getuid(), GID: os.Getgid()}); err != nil {
		t.Fatalf("write replaces the link, not the target: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(outside, "secret")); string(b) != "shadow" { //nolint:gosec // G304: the test's own temp folder.
		t.Fatalf("target changed: %q", b)
	}
	list, err := fsys.List(ctx(), areaIn(home, "."))
	if err != nil || len(list) != 1 {
		t.Fatalf("list %+v %v", list, err)
	}
}

// A listed link names where it points, as stored, without following it.
func TestFilesListNamesWhereALinkPoints(t *testing.T) {
	home := t.TempDir()
	if err := os.Mkdir(filepath.Join(home, "dist"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("dist", filepath.Join(home, "current")); err != nil {
		t.Fatal(err)
	}
	list, err := system.NewFiles().List(ctx(), areaIn(home, "."))
	if err != nil {
		t.Fatal(err)
	}
	targets := map[string]string{}
	for _, e := range list {
		targets[e.Name] = e.LinkTarget
	}
	if targets["current"] != "dist" || targets["dist"] != "" {
		t.Fatalf("link targets %v", targets)
	}
}

func TestFilesListMissingAndFile(t *testing.T) {
	base := t.TempDir()
	write(t, filepath.Join(base, "f"), "x")
	fsys := system.NewFiles()
	if _, err := fsys.List(ctx(), areaIn(base, "nope")); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
	if _, err := fsys.List(ctx(), areaIn(base, "f")); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("file: %v", err)
	}
	if err := fsys.Delete(ctx(), areaIn(base, "nope"), false); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("delete missing: %v", err)
	}
	if err := fsys.Delete(ctx(), areaIn(base, "f"), false); err != nil {
		t.Fatalf("delete file: %v", err)
	}
}

func TestPasswd(t *testing.T) {
	p := filepath.Join(t.TempDir(), "passwd")
	write(t, p, "root:x:0:0:root:/root:/bin/bash\n# comment\nbroken\nwriter:x:200001:200001::/home/agents/writer:/usr/sbin/nologin\n")
	users, err := system.NewPasswd(p).List(ctx())
	if err != nil {
		t.Fatal(err)
	}
	want := []domain.MachineUser{
		{Name: "root", UID: 0, GID: 0, Home: "/root", Shell: "/bin/bash", Kind: domain.UserSystem},
		{Name: "writer", UID: 200001, GID: 200001, Home: "/home/agents/writer", Shell: "/usr/sbin/nologin", Kind: domain.UserAgent},
	}
	if diff := cmp.Diff(want, users); diff != "" {
		t.Fatal(diff)
	}
	if _, err := system.NewPasswd(filepath.Join(t.TempDir(), "none")).List(ctx()); err == nil {
		t.Fatal("missing passwd accepted")
	}
}

func fakeProc(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	write(t, filepath.Join(root, "stat"), "cpu  1 2 3 4\nbtime 1700000000\n")
	write(t, filepath.Join(root, "uptime"), "1000.00 3000.00\n")
	write(t, filepath.Join(root, "7/stat"), "7 (sleep me) S 1 7 7 0 -1 0 0 0 0 0 300 200 0 0 20 0 1 0 50000 1000 100 0\n")
	write(t, filepath.Join(root, "7/status"), "Name:\tsleep\nUid:\t200001\t200001\t200001\t200001\n")
	write(t, filepath.Join(root, "7/cmdline"), "sleep\x00900\x00")
	write(t, filepath.Join(root, "self/stat"), "ignored")
	passwd := filepath.Join(t.TempDir(), "passwd")
	write(t, passwd, "writer:x:200001:200001::/home/agents/writer:/usr/sbin/nologin\n")
	return root, passwd
}

func TestProcessTable(t *testing.T) {
	root, passwd := fakeProc(t)
	table := system.NewProcs(root, system.NewPasswd(passwd), func(int, domain.Signal) error { return nil })
	list, err := table.List(ctx())
	if err != nil || len(list) != 1 {
		t.Fatalf("list %+v %v", list, err)
	}
	p := list[0]
	if p.PID != 7 || p.PPID != 1 || p.UID != 200001 || p.User != "writer" || p.Agent != "writer" || p.Command != "sleep 900" {
		t.Fatalf("process %+v", p)
	}
	if !p.Started.Equal(time.Unix(1700000000+500, 0).UTC()) {
		t.Fatalf("started %v", p.Started)
	}
	if p.RSSBytes != 100*int64(os.Getpagesize()) || p.CPUPercent <= 0 {
		t.Fatalf("rss %d cpu %v", p.RSSBytes, p.CPUPercent)
	}
	if _, err := table.Get(ctx(), 99); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("get missing: %v", err)
	}
	var sent []string
	table = system.NewProcs(root, system.NewPasswd(passwd), func(_ int, s domain.Signal) error {
		sent = append(sent, string(s))
		return nil
	})
	if err := table.Signal(ctx(), 7, domain.SignalHup); err != nil || len(sent) != 1 {
		t.Fatalf("signal %v %v", sent, err)
	}
}

func TestMetrics(t *testing.T) {
	root, _ := fakeProc(t)
	write(t, filepath.Join(root, "meminfo"), "MemTotal:       1000 kB\nMemFree:         100 kB\nMemAvailable:    400 kB\n")
	write(t, filepath.Join(root, "loadavg"), "0.50 0.25 0.10 1/100 7\n")
	write(t, filepath.Join(root, "net/dev"), "Inter-| head\n face |bytes\n    lo: 50 1 0 0 0 0 0 0 50 1 0 0 0 0 0 0\n  eth0: 1000 10 0 0 0 0 0 0 2000 20 0 0 0 0 0 0\n")
	cg := filepath.Join(t.TempDir(), "cpu.stat")
	write(t, cg, "usage_usec 5000000\nuser_usec 1\n")
	disk := t.TempDir()
	now := time.Unix(2000, 0)
	clock := func() time.Time { return now }
	r := system.NewMetrics(system.MetricsPaths{Proc: root, CPUStat: cg, Mounts: []string{disk}}, clock)
	m, err := r.Read(ctx())
	if err != nil {
		t.Fatal(err)
	}
	if m.MemTotal != 1000*1024 || m.MemAvail != 400*1024 || m.MemUsed != 600*1024 {
		t.Fatalf("memory %+v", m)
	}
	if m.Load != [3]float64{0.5, 0.25, 0.1} || m.Uptime != 1000*time.Second {
		t.Fatalf("load %v uptime %v", m.Load, m.Uptime)
	}
	if m.RxBytes != 1000 || m.TxBytes != 2000 {
		t.Fatalf("net %d %d", m.RxBytes, m.TxBytes)
	}
	if len(m.Disks) != 1 || m.Disks[0].Total <= 0 || m.Hostname == "" {
		t.Fatalf("disks %+v host %q", m.Disks, m.Hostname)
	}
	write(t, cg, "usage_usec 6000000\n")
	now = now.Add(time.Second)
	m, _ = r.Read(ctx())
	if m.CPUPercent <= 0 {
		t.Fatalf("cpu %v", m.CPUPercent)
	}
}

// runner records commands and answers from a script.
type runner struct {
	calls   []string
	answers map[string]string
	fail    map[string]error
}

func (r *runner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	line := name + " " + strings.Join(args, " ")
	r.calls = append(r.calls, line)
	if err := r.fail[name]; err != nil {
		return []byte("E: boom"), err
	}
	return []byte(r.answers[name]), nil
}

func TestApt(t *testing.T) {
	r := &runner{answers: map[string]string{
		"dpkg-query": "ii \tbash\t5.2\tarm64\tGNU shell\nrc \told\t1\tarm64\tremoved\nii \tjq\t1.7\tarm64\tJSON tool\n",
		"apt-mark":   "bash\njq\n",
	}}
	a := system.NewApt(r)
	pkgs, err := a.Installed(ctx())
	if err != nil || len(pkgs) != 2 || pkgs[1] != (domain.Package{Name: "jq", Version: "1.7", Architecture: "arm64", Summary: "JSON tool"}) {
		t.Fatalf("installed %+v %v", pkgs, err)
	}
	manual, err := a.Manual(ctx())
	if err != nil || !cmp.Equal(manual, []string{"bash", "jq"}) {
		t.Fatalf("manual %v %v", manual, err)
	}
	if err := a.Install(ctx(), "ripgrep", "14.1"); err != nil {
		t.Fatal(err)
	}
	if err := a.Install(ctx(), "fd-find", ""); err != nil {
		t.Fatal(err)
	}
	if err := a.Remove(ctx(), "jq"); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"apt-get install -y --no-install-recommends -- ripgrep=14.1",
		"apt-get install -y --no-install-recommends -- fd-find",
		"apt-get remove -y -- jq",
	}
	if diff := cmp.Diff(want, r.calls[2:]); diff != "" {
		t.Fatal(diff)
	}
	r.fail = map[string]error{"apt-get": errors.New("exit status 100")}
	if err := a.Install(ctx(), "x1", ""); err == nil || !strings.Contains(err.Error(), "E: boom") {
		t.Fatalf("install error %v", err)
	}
}

func TestRecipeStore(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "recipe")
	s := system.NewRecipeStore(dir)
	if _, found, err := s.Load(ctx()); err != nil || found {
		t.Fatalf("empty store: %v %v", found, err)
	}
	h, err := s.PutBlob(ctx(), []byte("port=80"))
	if err != nil || len(h) != 64 {
		t.Fatalf("blob %q %v", h, err)
	}
	b, err := s.Blob(ctx(), h)
	if err != nil || string(b) != "port=80" {
		t.Fatalf("read blob %q %v", b, err)
	}
	if _, err := s.Blob(ctx(), "../../etc/passwd"); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("traversal: %v", err)
	}
	r := domain.Recipe{Baseline: []string{"bash"}, Packages: []string{"jq"}, Files: []domain.FileRecord{{Path: "/etc/x", Mode: 0o600, UID: 0, GID: 0, Hash: h}}}
	if err := s.Save(ctx(), r); err != nil {
		t.Fatal(err)
	}
	got, found, err := system.NewRecipeStore(dir).Load(ctx())
	if err != nil || !found {
		t.Fatalf("load: %v %v", found, err)
	}
	if diff := cmp.Diff(r, got); diff != "" {
		t.Fatal(diff)
	}
	write(t, filepath.Join(dir, "recipe.json"), "{bad")
	if _, _, err := s.Load(ctx()); err == nil {
		t.Fatal("bad json accepted")
	}
}

func TestKill(t *testing.T) {
	if err := system.Kill(os.Getpid(), domain.SignalCont); err != nil {
		t.Fatalf("CONT to self: %v", err)
	}
	if err := system.Kill(1<<22, domain.SignalCont); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("missing pid: %v", err)
	}
	if err := system.Kill(os.Getpid(), "SEGV"); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("unknown signal: %v", err)
	}
}

func TestPowerTimer(_ *testing.T) {
	fired := make(chan struct{})
	system.Power{}.AfterFunc(0, func() { close(fired) })
	<-fired
}

func TestBaseline(t *testing.T) {
	p := filepath.Join(t.TempDir(), "baseline-packages.txt")
	if _, ok, err := system.NewBaseline(p).Baseline(ctx()); ok || err != nil {
		t.Fatalf("missing list: %v %v", ok, err)
	}
	write(t, p, "bash\n\n# image packages\ncoreutils\n")
	list, ok, err := system.NewBaseline(p).Baseline(ctx())
	if err != nil || !ok || !cmp.Equal(list, []string{"bash", "coreutils"}) {
		t.Fatalf("list %v %v %v", list, ok, err)
	}
}
