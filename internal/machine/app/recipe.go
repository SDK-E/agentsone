// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"fmt"

	"github.com/SDK-E/agentsone/internal/machine/domain"
)

// Reapply brings the machine back to its recipe: missing packages are
// installed and settings files restored. Every step runs even when an
// earlier one fails; failures land in the report and the audit log and never
// stop the daemon. Safe to run any number of times.
func (m *Machine) Reapply(ctx context.Context) domain.Report {
	m.recipeMu.Lock()
	defer m.recipeMu.Unlock()
	rep := domain.Report{State: domain.RecipeRunning, Started: m.d.Clock.Now()}
	m.report = rep
	r, err := m.loadOrStart(ctx)
	if err != nil {
		rep.Fail(domain.StepRecipe, "recipe", err.Error())
	} else {
		m.restorePackages(ctx, r, &rep)
		m.restoreFiles(ctx, r, &rep)
	}
	if rep.State == domain.RecipeRunning {
		rep.State = domain.RecipeApplied
	}
	rep.Finished = m.d.Clock.Now()
	m.report = rep
	m.auditReport(ctx, rep)
	return rep
}

// loadOrStart loads the recipe; on the very first start it records the
// baseline, from the image's list or, failing that, from apt's manual set.
func (m *Machine) loadOrStart(ctx context.Context) (domain.Recipe, error) {
	if err := ctx.Err(); err != nil {
		return domain.Recipe{}, fmt.Errorf("reapply stopped: %w", err)
	}
	r, found, err := m.d.Recipes.Load(ctx)
	if err != nil {
		return domain.Recipe{}, fmt.Errorf("load recipe: %w", err)
	}
	if found {
		return r, nil
	}
	baseline, ok, err := m.d.Baseline.Baseline(ctx)
	if err != nil {
		return domain.Recipe{}, fmt.Errorf("read image baseline: %w", err)
	}
	if !ok {
		if baseline, err = m.d.Packages.Manual(ctx); err != nil {
			return domain.Recipe{}, fmt.Errorf("snapshot baseline: %w", err)
		}
	}
	r = domain.Recipe{Baseline: baseline}
	if err := m.d.Recipes.Save(ctx, r); err != nil {
		return domain.Recipe{}, fmt.Errorf("save first recipe: %w", err)
	}
	return r, nil
}

func (m *Machine) restorePackages(ctx context.Context, r domain.Recipe, rep *domain.Report) {
	rep.Packages = len(r.Packages)
	installed, err := m.d.Packages.Installed(ctx)
	if err != nil {
		rep.Fail(domain.StepPackage, "dpkg", err.Error())
		return
	}
	names := make([]string, 0, len(installed))
	for _, p := range installed {
		names = append(names, p.Name)
	}
	for _, name := range r.MissingPackages(names) {
		if err := m.d.Packages.Install(ctx, name, ""); err != nil {
			rep.Fail(domain.StepPackage, name, err.Error())
		}
	}
}

func (m *Machine) restoreFiles(ctx context.Context, r domain.Recipe, rep *domain.Report) {
	rep.Files = len(r.Files)
	for _, f := range r.Files {
		if err := m.restoreFile(ctx, f); err != nil {
			rep.Fail(domain.StepFile, f.Path, err.Error())
		}
	}
}

func (m *Machine) restoreFile(ctx context.Context, f domain.FileRecord) error {
	data, err := m.d.Recipes.Blob(ctx, f.Hash)
	if err != nil {
		return fmt.Errorf("content: %w", err)
	}
	area, err := domain.AreaFor(daemon, f.Path)
	if err != nil {
		return err
	}
	w := FileWrite{Data: data, Mode: f.Mode, UID: f.UID, GID: f.GID, CreateParents: true}
	if _, err := m.d.Files.Write(ctx, area, w); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	return nil
}

func (m *Machine) auditReport(ctx context.Context, rep domain.Report) {
	action := "machine.recipe.applied"
	detail := map[string]any{"packages": rep.Packages, "files": rep.Files}
	if rep.State == domain.RecipeFailed {
		action = "machine.recipe.failed"
		var failures []string
		for _, f := range rep.Failures {
			failures = append(failures, fmt.Sprintf("%s %s: %s", f.Kind, f.Target, f.Reason))
		}
		detail["failures"] = failures
	}
	// The report itself carries the outcome; an audit failure must not turn
	// a good reapply into a failed one.
	_ = m.audit(ctx, daemon, action, "machine", detail)
}

// RecipeReport is the outcome of the last reapply.
func (m *Machine) RecipeReport() domain.Report {
	if m.recipeMu.TryLock() {
		defer m.recipeMu.Unlock()
		return m.report
	}
	return domain.Report{State: domain.RecipeRunning}
}

// SyncRecipe records packages installed or removed outside the API, from
// apt's manual set. It waits for the first reapply: before that the manual
// set is the bare image and syncing would wipe the recipe.
func (m *Machine) SyncRecipe(ctx context.Context) error {
	m.recipeMu.Lock()
	defer m.recipeMu.Unlock()
	if m.report.State != domain.RecipeApplied && m.report.State != domain.RecipeFailed {
		return nil
	}
	manual, err := m.d.Packages.Manual(ctx)
	if err != nil {
		return fmt.Errorf("read manual packages: %w", err)
	}
	return m.changeRecipeLocked(ctx, func(r *domain.Recipe) { r.SyncManual(manual) })
}
