// SPDX-License-Identifier: MIT

package domain_test

import (
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/SDK-E/agentsone/internal/machine/domain"
)

func TestRecipePackagesAreASortedSet(t *testing.T) {
	var r domain.Recipe
	r.AddPackage("nginx")
	r.AddPackage("curl")
	r.AddPackage("nginx")
	if diff := cmp.Diff([]string{"curl", "nginx"}, r.Packages); diff != "" {
		t.Fatal(diff)
	}
	r.RemovePackage("curl")
	r.RemovePackage("absent")
	if diff := cmp.Diff([]string{"nginx"}, r.Packages); diff != "" {
		t.Fatal(diff)
	}
}

func TestRecipeSkipsBaselinePackages(t *testing.T) {
	r := domain.Recipe{Baseline: []string{"bash", "curl"}}
	r.AddPackage("curl")
	if len(r.Packages) != 0 {
		t.Fatalf("baseline package recorded: %v", r.Packages)
	}
}

func TestRecipeSyncManualKeepsOnlyAddedPackages(t *testing.T) {
	r := domain.Recipe{Baseline: []string{"bash", "coreutils"}, Packages: []string{"gone"}}
	r.SyncManual([]string{"coreutils", "jq", "bash", "nginx"})
	if diff := cmp.Diff([]string{"jq", "nginx"}, r.Packages); diff != "" {
		t.Fatal(diff)
	}
}

func TestRecipeMissingPackages(t *testing.T) {
	r := domain.Recipe{Packages: []string{"jq", "nginx", "ripgrep"}}
	got := r.MissingPackages([]string{"bash", "nginx"})
	if diff := cmp.Diff([]string{"jq", "ripgrep"}, got); diff != "" {
		t.Fatal(diff)
	}
}

func TestRecipeFiles(t *testing.T) {
	var r domain.Recipe
	r.RecordFile(domain.FileRecord{Path: "/etc/b.conf", Mode: 0o644, Hash: "h1"})
	r.RecordFile(domain.FileRecord{Path: "/etc/a.conf", Mode: 0o600, Hash: "h2"})
	r.RecordFile(domain.FileRecord{Path: "/etc/b.conf", Mode: 0o640, Hash: "h3"})
	r.RecordFile(domain.FileRecord{Path: "/etc/nginx/sites/x", Hash: "h4"})
	want := []domain.FileRecord{
		{Path: "/etc/a.conf", Mode: 0o600, Hash: "h2"},
		{Path: "/etc/b.conf", Mode: 0o640, Hash: "h3"},
		{Path: "/etc/nginx/sites/x", Hash: "h4"},
	}
	if diff := cmp.Diff(want, r.Files); diff != "" {
		t.Fatal(diff)
	}
	r.ForgetPath("/etc/nginx")
	r.ForgetPath("/etc/a.conf")
	if diff := cmp.Diff(want[1:2], r.Files); diff != "" {
		t.Fatal(diff)
	}
	if !r.Uses("h3") || r.Uses("h2") {
		t.Fatal("Uses does not follow recorded files")
	}
}

func TestReportStatesAsService(t *testing.T) {
	start := time.Date(2026, 11, 19, 21, 0, 0, 0, time.UTC)
	pending := domain.Report{}
	if s := pending.Service(); s.Unit != domain.RecipeUnit || s.ActiveState != domain.ServiceInactive || s.SubState != "pending" {
		t.Fatalf("pending: %+v", s)
	}
	running := domain.Report{State: domain.RecipeRunning, Started: start}
	if s := running.Service(); s.ActiveState != domain.ServiceActivating {
		t.Fatalf("running: %+v", s)
	}
	ok := domain.Report{State: domain.RecipeApplied, Started: start, Finished: start.Add(time.Second), Packages: 2, Files: 1}
	s := ok.Service()
	if s.ActiveState != domain.ServiceActive || s.SubState != "exited" || !s.Enabled {
		t.Fatalf("applied: %+v", s)
	}
	if !strings.Contains(s.Description, "2 packages, 1 file") {
		t.Fatalf("description %q", s.Description)
	}
	bad := ok
	bad.Fail(domain.StepPackage, "jq", "apt: unable to locate")
	if bad.State != domain.RecipeFailed {
		t.Fatalf("state %s", bad.State)
	}
	s = bad.Service()
	if s.ActiveState != domain.ServiceFailed || !strings.Contains(s.Description, "package jq: apt: unable to locate") {
		t.Fatalf("failed: %+v", s)
	}
	two := bad
	two.Fail(domain.StepFile, "/etc/x", "denied")
	if !strings.Contains(two.Service().Description, "and 1 more") {
		t.Fatalf("two failures: %q", two.Service().Description)
	}
}

func TestParseServiceState(t *testing.T) {
	if s, err := domain.ParseServiceState("failed"); err != nil || s != domain.ServiceFailed {
		t.Fatalf("%v %v", s, err)
	}
	if _, err := domain.ParseServiceState("dead"); err == nil {
		t.Fatal("dead accepted")
	}
}
