// SPDX-License-Identifier: MIT

package app_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/machine/app"
	"github.com/SDK-E/agentsone/internal/machine/domain"
)

var errDisk = errors.New("disk on fire")

// brokenRecipes fails every call.
type brokenRecipes struct{}

func (brokenRecipes) Load(context.Context) (domain.Recipe, bool, error) {
	return domain.Recipe{}, false, errDisk
}
func (brokenRecipes) Save(context.Context, domain.Recipe) error       { return errDisk }
func (brokenRecipes) PutBlob(context.Context, []byte) (string, error) { return "", errDisk }
func (brokenRecipes) Blob(context.Context, string) ([]byte, error)    { return nil, errDisk }
func (brokenApt) Installed(context.Context) ([]domain.Package, error) { return nil, errDisk }
func (brokenApt) Manual(context.Context) ([]string, error)            { return nil, errDisk }
func (brokenApt) Install(context.Context, string, string) error       { return errDisk }
func (brokenApt) Remove(context.Context, string) error                { return errDisk }
func (brokenSystem) List(context.Context) ([]domain.Process, error)   { return nil, errDisk }
func (brokenSystem) Get(context.Context, int) (domain.Process, error) {
	return domain.Process{PID: 9}, nil
}
func (brokenSystem) Signal(context.Context, int, domain.Signal) error   { return errDisk }
func (brokenUsers) List(context.Context) ([]domain.MachineUser, error)  { return nil, errDisk }
func (brokenMetrics) Read(context.Context) (domain.Metrics, error)      { return domain.Metrics{}, errDisk }
func (brokenBaseline) Baseline(context.Context) ([]string, bool, error) { return nil, false, errDisk }

type (
	brokenApt      struct{}
	brokenSystem   struct{}
	brokenUsers    struct{}
	brokenMetrics  struct{}
	brokenBaseline struct{}
)

func TestPortFailuresAreWrapped(t *testing.T) {
	r := newRig(t, func(d *app.Deps) {
		d.Packages, d.Processes, d.Users, d.Metrics = brokenApt{}, brokenSystem{}, brokenUsers{}, brokenMetrics{}
	})
	checks := map[string]error{}
	_, _, checks["list packages"] = r.m.ListPackages(ctx(), "", "", 10)
	_, checks["remove"] = r.m.RemovePackage(ctx(), human, "jq")
	_, _, checks["list processes"] = r.m.ListProcesses(ctx(), app.ProcessFilter{}, "", 10)
	checks["signal"] = r.m.SignalProcess(ctx(), human, 9, "TERM")
	_, _, checks["users"] = r.m.ListUsers(ctx(), "", 10)
	_, checks["metrics"] = r.m.Metrics(ctx())
	for name, err := range checks {
		if !errors.Is(err, errDisk) {
			t.Errorf("%s: err = %v, want wrapped disk error", name, err)
		}
	}
}

func TestFileErrorsAreWrapped(t *testing.T) {
	r := newRig(t)
	if _, _, err := r.m.ListFiles(ctx(), human, "/nope", "", 10); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("list: %v", err)
	}
	if _, err := r.m.ReadFile(ctx(), human, "/nope"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("read: %v", err)
	}
	if _, err := r.m.ReadFile(ctx(), human, "rel"); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("read relative: %v", err)
	}
	if _, err := r.m.WriteFile(ctx(), human, app.WriteRequest{Path: "/no/parent/x"}); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("write: %v", err)
	}
	if err := r.m.DeleteFile(ctx(), human, "/nope", false); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("delete: %v", err)
	}
	r.fs.put("/srv/d/x", "")
	if err := r.m.DeleteFile(ctx(), human, "/srv/d", false); !errors.Is(err, core.ErrConflict) {
		t.Fatalf("delete folder: %v", err)
	}
}

func TestRecipeStoreFailures(t *testing.T) {
	r := newRig(t, func(d *app.Deps) { d.Recipes = brokenRecipes{} })
	if _, err := r.m.WriteFile(ctx(), human, app.WriteRequest{Path: "/etc.conf", Data: []byte("x")}); !errors.Is(err, errDisk) {
		t.Fatalf("write: %v", err)
	}
	r.fs.put("/etc/x.conf", "")
	if err := r.m.DeleteFile(ctx(), human, "/etc/x.conf", false); !errors.Is(err, errDisk) {
		t.Fatalf("delete: %v", err)
	}
	if rep := r.m.Reapply(ctx()); rep.State != domain.RecipeFailed {
		t.Fatalf("reapply %+v", rep)
	}
	job, _ := r.m.InstallPackage(ctx(), human, "jq", "")
	r.m.Drain(ctx())
	if got, _ := r.m.Job(job.ID); got.State != domain.JobFailed {
		t.Fatalf("job state %s", got.State)
	}
}

func TestFirstStartFailures(t *testing.T) {
	r := newRig(t, func(d *app.Deps) { d.Baseline = brokenBaseline{} })
	if rep := r.m.Reapply(ctx()); rep.State != domain.RecipeFailed {
		t.Fatalf("baseline failure: %+v", rep)
	}
	r = newRig(t, func(d *app.Deps) { d.Packages = brokenApt{} })
	if rep := r.m.Reapply(ctx()); rep.State != domain.RecipeFailed {
		t.Fatalf("manual failure: %+v", rep)
	}
	r = newRig(t, func(d *app.Deps) { d.Packages = brokenApt{} })
	_ = r.recipes.Save(ctx(), domain.Recipe{})
	if rep := r.m.Reapply(ctx()); rep.State != domain.RecipeFailed || rep.Failures[0].Target != "dpkg" {
		t.Fatalf("dpkg failure: %+v", rep)
	}
	if err := r.m.SyncRecipe(ctx()); !errors.Is(err, errDisk) {
		t.Fatalf("sync: %v", err)
	}
}

func TestQueueFullIsAConflict(t *testing.T) {
	r := newRig(t)
	var err error
	for i := range 65 {
		_, err = r.m.InstallPackage(ctx(), human, fmt.Sprintf("pkg%d", i), "")
	}
	if !errors.Is(err, core.ErrConflict) {
		t.Fatalf("65th job: %v", err)
	}
}

func TestEnqueueNeedsAudit(t *testing.T) {
	r := newRig(t, func(d *app.Deps) { d.Audit = failingAudit{} })
	if _, err := r.m.InstallPackage(ctx(), human, "jq", ""); err == nil {
		t.Fatal("queued without audit")
	}
}

func TestWorkRunsJobsUntilStopped(t *testing.T) {
	r := newRig(t)
	job, _ := r.m.InstallPackage(ctx(), human, "jq", "")
	c, cancel := context.WithCancel(ctx())
	done := make(chan struct{})
	go func() { r.m.Work(c); close(done) }()
	for {
		if got, _ := r.m.Job(job.ID); got.State == domain.JobDone {
			break
		}
	}
	cancel()
	<-done
}

func TestRecipeReportWhileApplying(t *testing.T) {
	r := newRig(t)
	block := make(chan struct{})
	release := make(chan struct{})
	r.fs.fail = nil
	r.recipes.blobs["h"] = []byte("x")
	_ = r.recipes.Save(ctx(), domain.Recipe{Files: []domain.FileRecord{{Path: "/etc/x", Hash: "h"}}})
	slow := &slowFS{fakeFS: r.fs, entered: block, release: release}
	r2 := newRig(t, func(d *app.Deps) { d.Files = slow; d.Recipes = r.recipes })
	go r2.m.Reapply(ctx())
	<-block
	if got := r2.m.RecipeReport().State; got != domain.RecipeRunning {
		t.Fatalf("state while applying %s", got)
	}
	close(release)
}

// slowFS blocks the first write until released.
type slowFS struct {
	*fakeFS
	entered chan struct{}
	release chan struct{}
}

func (s *slowFS) Write(c context.Context, a domain.Area, w app.FileWrite) (domain.FileEntry, error) {
	close(s.entered)
	<-s.release
	return s.fakeFS.Write(c, a, w)
}
