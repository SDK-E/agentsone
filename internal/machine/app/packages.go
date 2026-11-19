// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/machine/domain"
)

// ListPackages lists installed packages whose name holds q.
func (m *Machine) ListPackages(ctx context.Context, q, cursor string, limit int) ([]domain.Package, string, error) {
	all, err := m.d.Packages.Installed(ctx)
	if err != nil {
		return nil, "", fmt.Errorf("list packages: %w", err)
	}
	all = slices.DeleteFunc(all, func(p domain.Package) bool { return !strings.Contains(p.Name, q) })
	return domain.Page(all, cursor, limit)
}

// InstallPackage queues an apt install. Root agents and humans only.
func (m *Machine) InstallPackage(ctx context.Context, a domain.Actor, name, version string) (domain.Job, error) {
	if err := domain.CanManagePackages(a); err != nil {
		return domain.Job{}, err
	}
	if err := domain.ParsePackageName(name); err != nil {
		return domain.Job{}, err
	}
	if err := domain.ParsePackageVersion(version); err != nil {
		return domain.Job{}, err
	}
	return m.enqueue(ctx, a, domain.Job{Action: domain.JobInstall, Package: name, Version: version})
}

// RemovePackage queues an apt remove of an installed package.
func (m *Machine) RemovePackage(ctx context.Context, a domain.Actor, name string) (domain.Job, error) {
	if err := domain.CanManagePackages(a); err != nil {
		return domain.Job{}, err
	}
	if err := domain.ParsePackageName(name); err != nil {
		return domain.Job{}, err
	}
	installed, err := m.d.Packages.Installed(ctx)
	if err != nil {
		return domain.Job{}, fmt.Errorf("list packages: %w", err)
	}
	if !slices.ContainsFunc(installed, func(p domain.Package) bool { return p.Name == name }) {
		return domain.Job{}, fmt.Errorf("package %s: %w", name, core.ErrNotFound)
	}
	return m.enqueue(ctx, a, domain.Job{Action: domain.JobRemove, Package: name})
}

func (m *Machine) enqueue(ctx context.Context, a domain.Actor, j domain.Job) (domain.Job, error) {
	j.ID = m.d.IDs.New("job")
	j.State = domain.JobQueued
	j.Created = m.d.Clock.Now()
	j.By = a.Label()
	if err := m.audit(ctx, a, "machine.package."+string(j.Action), j.Package, map[string]any{"job": j.ID}); err != nil {
		return domain.Job{}, err
	}
	m.jobsMu.Lock()
	defer m.jobsMu.Unlock()
	select {
	case m.queue <- j.ID:
	default:
		return domain.Job{}, fmt.Errorf("package queue is full: %w", core.ErrConflict)
	}
	m.jobs[j.ID] = j
	return j, nil
}

// Job returns a package job by id.
func (m *Machine) Job(id string) (domain.Job, bool) {
	m.jobsMu.Lock()
	defer m.jobsMu.Unlock()
	j, ok := m.jobs[id]
	return j, ok
}

// Work runs queued package jobs one at a time until ctx ends.
func (m *Machine) Work(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case id := <-m.queue:
			m.run(ctx, id)
		}
	}
}

// Drain runs every job queued now and returns.
func (m *Machine) Drain(ctx context.Context) {
	for {
		select {
		case id := <-m.queue:
			m.run(ctx, id)
		default:
			return
		}
	}
}

func (m *Machine) run(ctx context.Context, id string) {
	j := m.setState(id, domain.JobRunning)
	var err error
	if j.Action == domain.JobInstall {
		err = m.d.Packages.Install(ctx, j.Package, j.Version)
	} else {
		err = m.d.Packages.Remove(ctx, j.Package)
	}
	if err == nil {
		err = m.changeRecipe(ctx, func(r *domain.Recipe) {
			if j.Action == domain.JobInstall {
				r.AddPackage(j.Package)
			} else {
				r.RemovePackage(j.Package)
			}
		})
	}
	actor := actorFromLabel(j.By)
	if err != nil {
		m.setState(id, domain.JobFailed)
		_ = m.audit(ctx, actor, "machine.package.failed", j.Package, map[string]any{"job": id, "error": err.Error()})
		return
	}
	m.setState(id, domain.JobDone)
	_ = m.audit(ctx, actor, doneAction[j.Action], j.Package, map[string]any{"job": id})
}

// doneAction is the audit action of a finished job, a fixed lookup.
var doneAction = map[domain.JobAction]string{
	domain.JobInstall: "machine.package.installed",
	domain.JobRemove:  "machine.package.removed",
}

func (m *Machine) setState(id string, s domain.JobState) domain.Job {
	m.jobsMu.Lock()
	defer m.jobsMu.Unlock()
	j := m.jobs[id]
	j.State = s
	m.jobs[id] = j
	return j
}

// actorFromLabel rebuilds the actor of a queued job for its audit records.
func actorFromLabel(label string) domain.Actor {
	kind, name, _ := strings.Cut(label, ":")
	return domain.Actor{Kind: domain.ActorKind(kind), Name: name}
}
