// SPDX-License-Identifier: MIT

package domain

import (
	"fmt"
	"io/fs"
	"slices"
	"strings"
	"time"
)

// Recipe is what agents added to the machine beyond the image: packages and
// settings files. The root file system resets to the image on every restart,
// so the daemon reapplies the recipe at start, before agents wake.
type Recipe struct {
	// Baseline is the image's own manually installed packages.
	Baseline []string
	// Packages are installed beyond the baseline, sorted.
	Packages []string
	// Files are settings files written outside the volumes, sorted by path.
	Files []FileRecord
}

// FileRecord is one settings file to restore. Its content is stored apart,
// by Hash.
type FileRecord struct {
	Path string
	Mode fs.FileMode
	UID  int
	GID  int
	Hash string
}

// AddPackage records a package installed beyond the baseline.
func (r *Recipe) AddPackage(name string) {
	if slices.Contains(r.Baseline, name) {
		return
	}
	if i, found := slices.BinarySearch(r.Packages, name); !found {
		r.Packages = slices.Insert(r.Packages, i, name)
	}
}

// RemovePackage forgets a removed package.
func (r *Recipe) RemovePackage(name string) {
	if i, found := slices.BinarySearch(r.Packages, name); found {
		r.Packages = slices.Delete(r.Packages, i, i+1)
	}
}

// SyncManual makes the packages equal apt's manual set minus the baseline,
// so installs and removals made from a terminal are kept too. Call it only
// after a reapply finished: before that the manual set is the bare image.
func (r *Recipe) SyncManual(manual []string) {
	r.Packages = nil
	for _, name := range manual {
		r.AddPackage(name)
	}
}

// MissingPackages are recipe packages not in installed, sorted.
func (r Recipe) MissingPackages(installed []string) []string {
	var missing []string
	for _, name := range r.Packages {
		if !slices.Contains(installed, name) {
			missing = append(missing, name)
		}
	}
	return missing
}

// RecordFile adds or replaces the record for f.Path.
func (r *Recipe) RecordFile(f FileRecord) {
	i, found := slices.BinarySearchFunc(r.Files, f.Path, byPath)
	if found {
		r.Files[i] = f
		return
	}
	r.Files = slices.Insert(r.Files, i, f)
}

// ForgetPath drops the record at p and every record below it.
func (r *Recipe) ForgetPath(p string) {
	r.Files = slices.DeleteFunc(r.Files, func(f FileRecord) bool { return within(f.Path, p) })
}

// Uses reports whether any record points at content hash h.
func (r Recipe) Uses(h string) bool {
	return slices.ContainsFunc(r.Files, func(f FileRecord) bool { return f.Hash == h })
}

func byPath(f FileRecord, p string) int { return strings.Compare(f.Path, p) }

// RecipeState is where the last reapply is.
type RecipeState string

// Recipe states.
const (
	RecipePending RecipeState = ""
	RecipeRunning RecipeState = "running"
	RecipeApplied RecipeState = "applied"
	RecipeFailed  RecipeState = "failed"
)

// StepKind is what a reapply step restores.
type StepKind string

// Step kinds.
const (
	StepPackage StepKind = "package"
	StepFile    StepKind = "file"
	StepRecipe  StepKind = "recipe"
)

// Failure is one step that did not apply.
type Failure struct {
	Kind   StepKind
	Target string
	Reason string
}

// Report is the outcome of the last reapply. A failure never stops the
// daemon; it shows on the machine-recipe service and in the audit log.
type Report struct {
	State    RecipeState
	Started  time.Time
	Finished time.Time
	Packages int
	Files    int
	Failures []Failure
}

// Fail records a step that did not apply.
func (r *Report) Fail(kind StepKind, target, reason string) {
	r.Failures = append(r.Failures, Failure{Kind: kind, Target: target, Reason: reason})
	r.State = RecipeFailed
}

// RecipeUnit is the service that shows the recipe in the services list.
// Restarting it reapplies the recipe.
const RecipeUnit = "machine-recipe.service"

// Service shows the report as the machine-recipe service.
func (r Report) Service() Service {
	s := Service{Unit: RecipeUnit, LoadState: "loaded", Enabled: true}
	switch r.State {
	case RecipePending:
		s.ActiveState, s.SubState = ServiceInactive, "pending"
		s.Description = "Machine recipe: not applied yet"
	case RecipeRunning:
		s.ActiveState, s.SubState = ServiceActivating, "start"
		s.Description = "Machine recipe: applying"
	case RecipeApplied:
		s.ActiveState, s.SubState = ServiceActive, "exited"
		s.Description = "Machine recipe: " + r.counts()
	case RecipeFailed:
		s.ActiveState, s.SubState = ServiceFailed, "failed"
		s.Description = "Machine recipe: " + r.counts() + "; " + r.firstFailure()
	}
	return s
}

func (r Report) counts() string {
	return fmt.Sprintf("%d %s, %d %s applied", r.Packages, plural(r.Packages, "package"), r.Files, plural(r.Files, "file"))
}

func (r Report) firstFailure() string {
	f := r.Failures[0]
	text := fmt.Sprintf("failed %s %s: %s", f.Kind, f.Target, f.Reason)
	if more := len(r.Failures) - 1; more > 0 {
		text += fmt.Sprintf(" and %d more", more)
	}
	return text
}

func plural(n int, word string) string {
	if n == 1 {
		return word
	}
	return word + "s"
}

// ServiceState is a service's active state, with the contract's values.
type ServiceState string

// Service states.
const (
	ServiceActive       ServiceState = "active"
	ServiceReloading    ServiceState = "reloading"
	ServiceInactive     ServiceState = "inactive"
	ServiceFailed       ServiceState = "failed"
	ServiceActivating   ServiceState = "activating"
	ServiceDeactivating ServiceState = "deactivating"
)

// ParseServiceState checks a state filter.
func ParseServiceState(s string) (ServiceState, error) {
	all := []ServiceState{ServiceActive, ServiceReloading, ServiceInactive, ServiceFailed, ServiceActivating, ServiceDeactivating}
	if !slices.Contains(all, ServiceState(s)) {
		return "", invalid("service state %q", s)
	}
	return ServiceState(s), nil
}

// Service is a long-running piece of the machine.
type Service struct {
	Unit        string
	Description string
	LoadState   string
	ActiveState ServiceState
	SubState    string
	Enabled     bool
	Agent       string
}
