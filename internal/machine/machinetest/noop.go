// SPDX-License-Identifier: MIT

// Package machinetest gives other packages' tests a machine module that
// never touches the host: no apt, no files, no /proc, no exit.
package machinetest

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/SDK-E/agentsone/internal/core"
	humandomain "github.com/SDK-E/agentsone/internal/humanuser/domain"
	ideapp "github.com/SDK-E/agentsone/internal/ide/app"
	"github.com/SDK-E/agentsone/internal/machine"
	"github.com/SDK-E/agentsone/internal/machine/app"
	"github.com/SDK-E/agentsone/internal/machine/domain"
)

// NoopSystem is a container with nothing on it. The recipe is empty and
// stored in memory, so a reapply ends at once and opens the startup gate.
func NoopSystem() machine.System {
	return machine.System{
		Packages: Packages{}, Files: Files{}, Processes: Processes{}, Users: Users{}, Pty: Pty{},
		Metrics: Metrics{}, Recipes: &Recipes{}, Baseline: Baseline{}, Power: Power{}, Timer: Power{},
		HumanAccounts: HumanAccounts{}, Editor: Editor{}, EditorVersion: "test",
	}
}

// Editor starts no editor server: every start fails as unavailable.
type Editor struct{}

// Launch refuses.
func (Editor) Launch(context.Context, ideapp.Spec) (ideapp.Process, error) {
	return nil, fmt.Errorf("no editor in the noop machine: %w", core.ErrUnavailable)
}

// HumanAccounts makes no Linux users and says each one is made.
type HumanAccounts struct{}

// Ensure does nothing.
func (HumanAccounts) Ensure(context.Context, humandomain.User) error { return nil }

// New builds a machine module on NoopSystem.
func New(deps core.Deps) *machine.Module { return machine.NewWith(deps, NoopSystem()) }

// Packages has nothing installed; installs and removals succeed and change
// nothing.
type Packages struct{}

// Installed lists nothing.
func (Packages) Installed(context.Context) ([]domain.Package, error) { return nil, nil }

// Manual lists nothing.
func (Packages) Manual(context.Context) ([]string, error) { return nil, nil }

// Install does nothing.
func (Packages) Install(context.Context, string, string) error { return nil }

// Remove does nothing.
func (Packages) Remove(context.Context, string) error { return nil }

// Files is an empty file system that refuses writes.
type Files struct{}

func notFound(a domain.Area) error { return fmt.Errorf("%s: %w", a.Path, core.ErrNotFound) }

// List finds nothing.
func (Files) List(_ context.Context, a domain.Area) ([]domain.FileEntry, error) {
	return nil, notFound(a)
}

// Read finds nothing.
func (Files) Read(_ context.Context, a domain.Area, _ int64) ([]byte, int64, error) {
	return nil, 0, notFound(a)
}

// Write refuses.
func (Files) Write(_ context.Context, a domain.Area, _ app.FileWrite) (domain.FileEntry, error) {
	return domain.FileEntry{}, notFound(a)
}

// Delete finds nothing.
func (Files) Delete(_ context.Context, a domain.Area, _ bool) error { return notFound(a) }

// Processes is an empty process table.
type Processes struct{}

// List lists nothing.
func (Processes) List(context.Context) ([]domain.Process, error) { return nil, nil }

// Get finds nothing.
func (Processes) Get(_ context.Context, pid int) (domain.Process, error) {
	return domain.Process{}, fmt.Errorf("pid %d: %w", pid, core.ErrNotFound)
}

// Signal finds nothing.
func (Processes) Signal(_ context.Context, pid int, _ domain.Signal) error {
	return fmt.Errorf("pid %d: %w", pid, core.ErrNotFound)
}

// Users has root only.
type Users struct{}

// List returns root.
func (Users) List(context.Context) ([]domain.MachineUser, error) {
	return []domain.MachineUser{{Name: "root", Home: "/root", Shell: "/bin/sh", Kind: domain.UserSystem}}, nil
}

// Pty starts no shell: a test that needs a terminal brings its own starter.
type Pty struct{}

// Start refuses.
func (Pty) Start(context.Context, domain.PtySpec, int, int, string, core.ProcessCell) (app.PtyProcess, error) {
	return nil, fmt.Errorf("the no-op machine has no terminals: %w", core.ErrUnavailable)
}

// Metrics reads an idle one-core machine.
type Metrics struct{}

// Read returns the idle snapshot.
func (Metrics) Read(context.Context) (domain.Metrics, error) {
	return domain.Metrics{Hostname: "machine", Cores: 1}, nil
}

// Recipes keeps the recipe in memory.
type Recipes struct {
	mu     sync.Mutex
	recipe *domain.Recipe
	blobs  map[string][]byte
}

// Load returns the saved recipe.
func (r *Recipes) Load(context.Context) (domain.Recipe, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.recipe == nil {
		return domain.Recipe{}, false, nil
	}
	return *r.recipe, true, nil
}

// Save keeps the recipe.
func (r *Recipes) Save(_ context.Context, rec domain.Recipe) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.recipe = &rec
	return nil
}

// PutBlob keeps data under a counter name.
func (r *Recipes) PutBlob(_ context.Context, data []byte) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.blobs == nil {
		r.blobs = map[string][]byte{}
	}
	h := fmt.Sprintf("blob%d", len(r.blobs))
	r.blobs[h] = data
	return h, nil
}

// Blob returns kept data.
func (r *Recipes) Blob(_ context.Context, h string) ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if b, ok := r.blobs[h]; ok {
		return b, nil
	}
	return nil, fmt.Errorf("blob %s: %w", h, core.ErrNotFound)
}

// Baseline is an image with an empty package list.
type Baseline struct{}

// Baseline returns the empty list.
func (Baseline) Baseline(context.Context) ([]string, bool, error) { return nil, true, nil }

// Power never ends the process and never fires timers.
type Power struct{}

// Exit does nothing.
func (Power) Exit(int) {}

// AfterFunc does nothing.
func (Power) AfterFunc(time.Duration, func()) {}
