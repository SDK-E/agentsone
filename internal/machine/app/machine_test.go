// SPDX-License-Identifier: MIT

package app_test

import (
	"context"
	"errors"
	"io/fs"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/core/coretest"
	"github.com/SDK-E/agentsone/internal/machine/app"
	"github.com/SDK-E/agentsone/internal/machine/domain"
)

var (
	human  = domain.Actor{Kind: domain.ActorHuman, Name: "alice", UID: -1}
	rootAg = domain.Actor{Kind: domain.ActorAgent, Name: "founder", UID: 0, IsRoot: true}
	custom = domain.Actor{Kind: domain.ActorAgent, Name: "writer", UID: 200001, Home: "/home/agents/writer"}
)

type rig struct {
	m       *app.Machine
	apt     *fakeApt
	fs      *fakeFS
	procs   *fakeProcs
	recipes *fakeRecipes
	power   *fakePower
	audit   *coretest.Audit
	clock   *coretest.Clock
}

func newRig(t *testing.T, opts ...func(*app.Deps)) *rig {
	t.Helper()
	r := &rig{
		apt: newFakeApt("bash", "coreutils"), fs: newFakeFS(), procs: &fakeProcs{},
		recipes: newFakeRecipes(), power: &fakePower{}, audit: coretest.NewAudit(),
		clock: coretest.NewClock(coretest.Epoch),
	}
	deps := app.Deps{
		Packages: r.apt, Files: r.fs, Processes: r.procs, Recipes: r.recipes,
		Baseline: fakeBaseline{}, Power: r.power, Timer: r.power,
		Users: fakeUsers{
			{Name: "root", UID: 0, GID: 0, Home: "/root", Shell: "/bin/bash", Kind: domain.UserSystem},
			{Name: "writer", UID: 200001, GID: 200001, Home: "/home/agents/writer", Shell: "/usr/sbin/nologin", Kind: domain.UserAgent},
		},
		Metrics: fakeMetrics{domain.Metrics{Hostname: "machine", Cores: 4}},
		Audit:   r.audit, Clock: r.clock, IDs: coretest.NewIDs(),
	}
	for _, o := range opts {
		o(&deps)
	}
	r.m = app.New(deps)
	return r
}

func (r *rig) actions() []string {
	var out []string
	for _, e := range r.audit.Entries() {
		out = append(out, e.Actor+" "+e.Action+" "+e.Target)
	}
	return out
}

func ctx() context.Context { return context.Background() }

func TestCustomAgentFilesStayInItsAreas(t *testing.T) {
	r := newRig(t)
	r.fs.put("/home/agents/writer/notes.txt", "hello")
	r.fs.put("/etc/shadow", "secret")
	if _, err := r.m.ReadFile(ctx(), custom, "/etc/shadow"); !errors.Is(err, core.ErrForbidden) {
		t.Fatalf("read /etc/shadow: %v", err)
	}
	c, err := r.m.ReadFile(ctx(), custom, "/home/agents/writer/notes.txt")
	if err != nil || string(c.Data) != "hello" || c.Truncated || c.Size != 5 {
		t.Fatalf("read own: %+v %v", c, err)
	}
	if _, err := r.m.WriteFile(ctx(), custom, app.WriteRequest{Path: "/etc/motd", Data: []byte("x")}); !errors.Is(err, core.ErrForbidden) {
		t.Fatalf("write /etc: %v", err)
	}
	if err := r.m.DeleteFile(ctx(), custom, "/etc/shadow", false); !errors.Is(err, core.ErrForbidden) {
		t.Fatalf("delete /etc: %v", err)
	}
	if _, _, err := r.m.ListFiles(ctx(), custom, "/etc", "", 10); !errors.Is(err, core.ErrForbidden) {
		t.Fatalf("list /etc: %v", err)
	}
}

func TestWriteFileRunsAsTheCaller(t *testing.T) {
	r := newRig(t)
	r.fs.put("/home/agents/writer/.keep", "")
	e, err := r.m.WriteFile(ctx(), custom, app.WriteRequest{Path: "/home/agents/writer/a.txt", Data: []byte("abc")})
	if err != nil || e.Size != 3 {
		t.Fatalf("write: %+v %v", e, err)
	}
	f, _ := r.fs.get("/home/agents/writer/a.txt")
	if f.uid != 200001 || f.gid != 200001 || f.mode != 0o644 {
		t.Fatalf("owner %d:%d mode %o", f.uid, f.gid, f.mode)
	}
	if got := r.recipes.Current().Files; len(got) != 0 {
		t.Fatalf("home file recorded in recipe: %v", got)
	}
	if diff := cmp.Diff([]string{"agent:writer machine.file.write /home/agents/writer/a.txt"}, r.actions()); diff != "" {
		t.Fatal(diff)
	}
}

func TestWriteFileRefusesOverCap(t *testing.T) {
	r := newRig(t)
	big := make([]byte, domain.MaxFileBytes+1)
	if _, err := r.m.WriteFile(ctx(), human, app.WriteRequest{Path: "/tmp/big", Data: big}); !errors.Is(err, domain.ErrTooLarge) {
		t.Fatalf("err = %v", err)
	}
}

func TestReadFileStopsAtCap(t *testing.T) {
	r := newRig(t)
	r.fs.put("/tmp/big", strings.Repeat("x", domain.MaxFileBytes+10))
	c, err := r.m.ReadFile(ctx(), human, "/tmp/big")
	if err != nil || !c.Truncated || len(c.Data) != domain.MaxFileBytes || c.Size != domain.MaxFileBytes+10 {
		t.Fatalf("truncated=%v len=%d size=%d err=%v", c.Truncated, len(c.Data), c.Size, err)
	}
}

func TestSettingsFilesJoinTheRecipe(t *testing.T) {
	r := newRig(t)
	mode := domainMode(0o600)
	_, err := r.m.WriteFile(ctx(), rootAg, app.WriteRequest{Path: "/etc/app/app.conf", Data: []byte("port=80"), Mode: &mode, CreateParents: true})
	if err != nil {
		t.Fatal(err)
	}
	files := r.recipes.Current().Files
	if len(files) != 1 || files[0].Path != "/etc/app/app.conf" || files[0].Mode != 0o600 || files[0].UID != 0 {
		t.Fatalf("recipe files: %+v", files)
	}
	if b, _ := r.recipes.Blob(ctx(), files[0].Hash); string(b) != "port=80" {
		t.Fatalf("blob %q", b)
	}
	if err := r.m.DeleteFile(ctx(), rootAg, "/etc/app", true); err != nil {
		t.Fatal(err)
	}
	if got := r.recipes.Current().Files; len(got) != 0 {
		t.Fatalf("deleted file kept in recipe: %v", got)
	}
}

func TestListFilesFoldersFirstThenByName(t *testing.T) {
	r := newRig(t)
	r.fs.put("/srv/b.txt", "")
	r.fs.put("/srv/a.txt", "")
	r.fs.put("/srv/zdir/x", "")
	page, next, err := r.m.ListFiles(ctx(), human, "/srv", "", 2)
	if err != nil || next == "" {
		t.Fatalf("page: %v %q", err, next)
	}
	if page[0].Name != "zdir" || page[1].Name != "a.txt" {
		t.Fatalf("order: %v, %v", page[0].Name, page[1].Name)
	}
}

func TestPackagesRootAgentsAndHumansOnly(t *testing.T) {
	r := newRig(t)
	if _, err := r.m.InstallPackage(ctx(), custom, "jq", ""); !errors.Is(err, core.ErrForbidden) {
		t.Fatalf("custom install: %v", err)
	}
	if _, err := r.m.RemovePackage(ctx(), custom, "bash"); !errors.Is(err, core.ErrForbidden) {
		t.Fatalf("custom remove: %v", err)
	}
	if _, err := r.m.InstallPackage(ctx(), human, "jq; rm -rf /", ""); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("bad name: %v", err)
	}
	if _, err := r.m.InstallPackage(ctx(), human, "jq", "1 -y"); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("bad version: %v", err)
	}
	if _, err := r.m.RemovePackage(ctx(), human, "absent"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("remove absent: %v", err)
	}
}

func TestInstallQueuesThenRecordsInRecipe(t *testing.T) {
	r := newRig(t)
	job, err := r.m.InstallPackage(ctx(), rootAg, "jq", "1.7")
	if err != nil || job.State != domain.JobQueued || job.Action != domain.JobInstall || job.Package != "jq" {
		t.Fatalf("job %+v %v", job, err)
	}
	if len(r.apt.Calls()) != 0 {
		t.Fatal("installed before the worker ran")
	}
	r.m.Drain(ctx())
	if got, _ := r.m.Job(job.ID); got.State != domain.JobDone {
		t.Fatalf("state %s", got.State)
	}
	if diff := cmp.Diff([]string{"install jq=1.7"}, r.apt.Calls()); diff != "" {
		t.Fatal(diff)
	}
	if diff := cmp.Diff([]string{"jq"}, r.recipes.Current().Packages); diff != "" {
		t.Fatal(diff)
	}
	rm, err := r.m.RemovePackage(ctx(), human, "jq")
	if err != nil {
		t.Fatal(err)
	}
	r.m.Drain(ctx())
	if got, _ := r.m.Job(rm.ID); got.State != domain.JobDone {
		t.Fatalf("remove state %s", got.State)
	}
	if got := r.recipes.Current().Packages; len(got) != 0 {
		t.Fatalf("removed package kept: %v", got)
	}
	want := []string{
		"agent:founder machine.package.install jq",
		"agent:founder machine.package.installed jq",
		"human:alice machine.package.remove jq",
		"human:alice machine.package.removed jq",
	}
	if diff := cmp.Diff(want, r.actions()); diff != "" {
		t.Fatal(diff)
	}
}

func TestFailedInstallIsReportedNotRecorded(t *testing.T) {
	r := newRig(t)
	r.apt.broken["nope"] = "unable to locate package nope"
	job, _ := r.m.InstallPackage(ctx(), human, "nope", "")
	r.m.Drain(ctx())
	if got, _ := r.m.Job(job.ID); got.State != domain.JobFailed {
		t.Fatalf("state %s", got.State)
	}
	if len(r.recipes.Current().Packages) != 0 {
		t.Fatal("failed package recorded")
	}
	if last := r.actions()[len(r.actions())-1]; last != "human:alice machine.package.failed nope" {
		t.Fatalf("last audit %q", last)
	}
	if _, ok := r.m.Job("unknown"); ok {
		t.Fatal("unknown job found")
	}
}

func TestListPackagesFiltersByName(t *testing.T) {
	r := newRig(t)
	page, _, err := r.m.ListPackages(ctx(), "core", "", 50)
	if err != nil || len(page) != 1 || page[0].Name != "coreutils" {
		t.Fatalf("page %v %v", page, err)
	}
}

func TestSignalPermissions(t *testing.T) {
	r := newRig(t)
	r.procs.procs = []domain.Process{{PID: 1, UID: 0}, {PID: 40, UID: 200001}, {PID: 41, UID: 200002}}
	if err := r.m.SignalProcess(ctx(), custom, 41, "TERM"); !errors.Is(err, core.ErrForbidden) {
		t.Fatalf("other's process: %v", err)
	}
	if err := r.m.SignalProcess(ctx(), custom, 40, "BOGUS"); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("bad signal: %v", err)
	}
	if err := r.m.SignalProcess(ctx(), custom, 99, "TERM"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("missing pid: %v", err)
	}
	if err := r.m.SignalProcess(ctx(), custom, 40, "TERM"); err != nil {
		t.Fatal(err)
	}
	if err := r.m.SignalProcess(ctx(), rootAg, 41, "KILL"); err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff([]string{"TERM 40", "KILL 41"}, r.procs.signals); diff != "" {
		t.Fatal(diff)
	}
	if got := r.actions(); len(got) != 2 || got[1] != "agent:founder machine.process.signal 41" {
		t.Fatalf("audit %v", got)
	}
}

func TestListProcessesHighestCPUFirstWithFilters(t *testing.T) {
	r := newRig(t)
	r.procs.procs = []domain.Process{
		{PID: 2, User: "root", CPUPercent: 1},
		{PID: 3, User: "writer", Agent: "writer", CPUPercent: 9},
		{PID: 4, User: "writer", Agent: "writer", CPUPercent: 5},
	}
	page, _, err := r.m.ListProcesses(ctx(), app.ProcessFilter{}, "", 10)
	if err != nil || page[0].PID != 3 || page[1].PID != 4 || page[2].PID != 2 {
		t.Fatalf("order %v %v", page, err)
	}
	page, _, _ = r.m.ListProcesses(ctx(), app.ProcessFilter{User: "root"}, "", 10)
	if len(page) != 1 || page[0].PID != 2 {
		t.Fatalf("user filter %v", page)
	}
	page, _, _ = r.m.ListProcesses(ctx(), app.ProcessFilter{Agent: "writer"}, "", 10)
	if len(page) != 2 {
		t.Fatalf("agent filter %v", page)
	}
}

func TestUsersAndMetrics(t *testing.T) {
	r := newRig(t)
	users, next, err := r.m.ListUsers(ctx(), "", 1)
	if err != nil || len(users) != 1 || next == "" || users[0].Name != "root" {
		t.Fatalf("users %v %q %v", users, next, err)
	}
	m, err := r.m.Metrics(ctx())
	if err != nil || m.Hostname != "machine" || !m.Time.Equal(coretest.Epoch) {
		t.Fatalf("metrics %+v %v", m, err)
	}
}

func TestRebootAuditsFirstThenExitsWithRebootCode(t *testing.T) {
	r := newRig(t)
	delay := 5
	got, err := r.m.Reboot(ctx(), rootAg, "apply kernel settings", &delay)
	if err != nil {
		t.Fatal(err)
	}
	if !got.At.Equal(coretest.Epoch.Add(5 * time.Second)) {
		t.Fatalf("at %v", got.At)
	}
	if diff := cmp.Diff([]string{"agent:founder machine.reboot machine"}, r.actions()); diff != "" {
		t.Fatal(diff)
	}
	if len(r.power.Exits()) != 0 {
		t.Fatal("exited before the delay")
	}
	if r.power.timers[0].d != 5*time.Second {
		t.Fatalf("timer %v", r.power.timers[0].d)
	}
	r.power.Fire()
	if diff := cmp.Diff([]int{domain.RebootExitCode}, r.power.Exits()); diff != "" {
		t.Fatal(diff)
	}
}

func TestRebootRefusedWhenAuditFails(t *testing.T) {
	r := newRig(t, func(d *app.Deps) { d.Audit = failingAudit{} })
	if _, err := r.m.Reboot(ctx(), human, "x", nil); err == nil {
		t.Fatal("reboot without an audit record")
	}
	if len(r.power.timers) != 0 {
		t.Fatal("timer set without audit")
	}
	if _, err := r.m.Reboot(ctx(), human, "", nil); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("empty reason: %v", err)
	}
}

func TestShutdownBelongsToHumans(t *testing.T) {
	r := newRig(t)
	err := r.m.Shutdown(ctx(), human)
	if !errors.Is(err, core.ErrForbidden) || !strings.Contains(err.Error(), "Docker or Coolify") {
		t.Fatalf("shutdown: %v", err)
	}
}

func TestServicesShowDaemonAndRecipe(t *testing.T) {
	r := newRig(t)
	list, _, err := r.m.ListServices(ctx(), "", "", 10)
	if err != nil || len(list) != 2 || list[0].Unit != app.DaemonUnit || list[1].Unit != domain.RecipeUnit {
		t.Fatalf("services %+v %v", list, err)
	}
	active, _, _ := r.m.ListServices(ctx(), domain.ServiceActive, "", 10)
	if len(active) != 1 || active[0].Unit != app.DaemonUnit {
		t.Fatalf("active filter %+v", active)
	}
	if _, err := r.m.StopService(ctx(), human, app.DaemonUnit); !errors.Is(err, core.ErrForbidden) {
		t.Fatalf("stop daemon: %v", err)
	}
	if _, err := r.m.StartService(ctx(), human, "nginx.service"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("unknown unit: %v", err)
	}
	if _, err := r.m.RestartService(ctx(), custom, domain.RecipeUnit); !errors.Is(err, core.ErrForbidden) {
		t.Fatalf("custom restarts recipe: %v", err)
	}
	if _, err := r.m.StopService(ctx(), human, domain.RecipeUnit); !errors.Is(err, core.ErrConflict) {
		t.Fatalf("stop recipe: %v", err)
	}
	s, err := r.m.RestartService(ctx(), human, domain.RecipeUnit)
	if err != nil || s.ActiveState != domain.ServiceActive {
		t.Fatalf("restart recipe: %+v %v", s, err)
	}
	s, err = r.m.StartService(ctx(), rootAg, domain.RecipeUnit)
	if err != nil || s.ActiveState != domain.ServiceActive {
		t.Fatalf("start recipe: %+v %v", s, err)
	}
	if _, err := r.m.RestartService(ctx(), human, app.DaemonUnit); !errors.Is(err, core.ErrForbidden) {
		t.Fatalf("restart daemon: %v", err)
	}
}

func TestFirstStartSnapshotsBaseline(t *testing.T) {
	r := newRig(t)
	rep := r.m.Reapply(ctx())
	if rep.State != domain.RecipeApplied {
		t.Fatalf("state %s %+v", rep.State, rep.Failures)
	}
	if diff := cmp.Diff([]string{"bash", "coreutils"}, r.recipes.Current().Baseline); diff != "" {
		t.Fatal(diff)
	}
}

func TestImageBaselineWins(t *testing.T) {
	r := newRig(t, func(d *app.Deps) { d.Baseline = fakeBaseline{list: []string{"bash"}, ok: true} })
	r.m.Reapply(ctx())
	rec := r.recipes.Current()
	if diff := cmp.Diff([]string{"bash"}, rec.Baseline); diff != "" {
		t.Fatal(diff)
	}
}

func TestReapplyRestoresPackagesAndFilesIdempotently(t *testing.T) {
	r := newRig(t)
	h, _ := r.recipes.PutBlob(ctx(), []byte("port=80"))
	_ = r.recipes.Save(ctx(), domain.Recipe{
		Baseline: []string{"bash", "coreutils"},
		Packages: []string{"jq", "nginx"},
		Files:    []domain.FileRecord{{Path: "/etc/app/app.conf", Mode: 0o600, UID: 0, GID: 0, Hash: h}},
	})
	r.apt.installed["nginx"], r.apt.manual["nginx"] = true, true
	rep := r.m.Reapply(ctx())
	if rep.State != domain.RecipeApplied || rep.Packages != 2 || rep.Files != 1 {
		t.Fatalf("report %+v", rep)
	}
	if diff := cmp.Diff([]string{"install jq="}, r.apt.Calls()); diff != "" {
		t.Fatal(diff)
	}
	f, ok := r.fs.get("/etc/app/app.conf")
	if !ok || string(f.data) != "port=80" || f.mode != 0o600 {
		t.Fatalf("file %+v", f)
	}
	again := r.m.Reapply(ctx())
	if again.State != domain.RecipeApplied || len(r.apt.Calls()) != 1 {
		t.Fatalf("second reapply %+v calls %v", again, r.apt.Calls())
	}
	if s := r.m.RecipeReport().Service(); s.ActiveState != domain.ServiceActive {
		t.Fatalf("service %+v", s)
	}
}

func TestReapplyFailuresAreReportedAndDoNotStopOtherSteps(t *testing.T) {
	r := newRig(t)
	_ = r.recipes.Save(ctx(), domain.Recipe{
		Baseline: []string{"bash"},
		Packages: []string{"broken", "jq"},
		Files:    []domain.FileRecord{{Path: "/etc/lost.conf", Hash: "missing"}},
	})
	r.apt.broken["broken"] = "unable to locate package broken"
	rep := r.m.Reapply(ctx())
	if rep.State != domain.RecipeFailed || len(rep.Failures) != 2 {
		t.Fatalf("report %+v", rep)
	}
	if !slices.Contains(r.apt.Calls(), "install jq=") {
		t.Fatalf("jq skipped after a failure: %v", r.apt.Calls())
	}
	if rep.Failures[0].Target != "broken" || rep.Failures[1].Target != "/etc/lost.conf" {
		t.Fatalf("failures %+v", rep.Failures)
	}
	acts := r.actions()
	if acts[len(acts)-1] != "system:agentsoned machine.recipe.failed machine" {
		t.Fatalf("audit %v", acts)
	}
	if got := r.recipes.Current().Packages; !slices.Contains(got, "broken") {
		t.Fatalf("failed package dropped from recipe: %v", got)
	}
}

func TestReapplyStopsAtDeadline(t *testing.T) {
	r := newRig(t)
	_ = r.recipes.Save(ctx(), domain.Recipe{Packages: []string{"jq"}})
	c, cancel := context.WithCancel(ctx())
	cancel()
	rep := r.m.Reapply(c)
	if rep.State != domain.RecipeFailed || rep.Failures[0].Kind != domain.StepRecipe {
		t.Fatalf("report %+v", rep)
	}
}

func TestReapplyFileWriteFailureIsReported(t *testing.T) {
	r := newRig(t)
	h, _ := r.recipes.PutBlob(ctx(), []byte("x"))
	_ = r.recipes.Save(ctx(), domain.Recipe{Files: []domain.FileRecord{{Path: "/etc/x", Hash: h}}})
	r.fs.fail = errors.New("read-only file system")
	rep := r.m.Reapply(ctx())
	if rep.State != domain.RecipeFailed || !strings.Contains(rep.Failures[0].Reason, "read-only") {
		t.Fatalf("report %+v", rep)
	}
}

func TestSyncRecipeOnlyAfterReapply(t *testing.T) {
	r := newRig(t)
	_ = r.recipes.Save(ctx(), domain.Recipe{Baseline: []string{"bash", "coreutils"}, Packages: []string{"jq"}})
	if err := r.m.SyncRecipe(ctx()); err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff([]string{"jq"}, r.recipes.Current().Packages); diff != "" {
		t.Fatalf("sync before reapply wiped the recipe: %s", diff)
	}
	r.m.Reapply(ctx())
	r.apt.installed["ripgrep"], r.apt.manual["ripgrep"] = true, true
	if err := r.m.SyncRecipe(ctx()); err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff([]string{"jq", "ripgrep"}, r.recipes.Current().Packages); diff != "" {
		t.Fatal(diff)
	}
}

func domainMode(m uint32) fs.FileMode { return fs.FileMode(m) }
