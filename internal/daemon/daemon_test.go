// SPDX-License-Identifier: MIT

package daemon_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/core/coretest"
	"github.com/SDK-E/agentsone/internal/daemon"
)

// A module that fails to start stops the daemon: the modules that did start
// stop again, in reverse order, and the failed one is not stopped.
func TestServe_StopsStartedModulesInReverseWhenOneFailsToStart(t *testing.T) {
	t.Parallel()
	deps, _ := coretest.NewDeps(t)
	var order []string
	mods := []core.Module{
		&recorder{name: "a", order: &order},
		&recorder{name: "b", order: &order},
		&recorder{name: "c", order: &order, failStart: true},
	}

	err := daemon.Serve(t.Context(), deps, mods, nil)

	if !errors.Is(err, errBoom) {
		t.Fatalf("Serve() error = %v, want %v", err, errBoom)
	}
	want := []string{"provide a", "provide b", "provide c", "migrate a", "migrate b", "migrate c",
		"start a", "start b", "start c", "stop b", "stop a"}
	if !slices.Equal(order, want) {
		t.Fatalf("order %v, want %v", order, want)
	}
}

// Every module the daemon runs has a distinct name: names are the
// migration namespaces.
func TestModules_HaveDistinctNames(t *testing.T) {
	t.Parallel()
	deps, _ := coretest.NewDeps(t)
	seen := map[string]bool{}
	for _, m := range daemon.Modules(deps) {
		if seen[m.Name()] {
			t.Fatalf("module name %q twice", m.Name())
		}
		seen[m.Name()] = true
	}
}

var errBoom = errors.New("boom")

type recorder struct {
	core.Base
	name      string
	order     *[]string
	failStart bool
}

func (r *recorder) Name() string { return r.name }
func (r *recorder) Provide(*core.Ports) {
	*r.order = append(*r.order, "provide "+r.name)
}
func (r *recorder) Migrate(context.Context) error {
	*r.order = append(*r.order, "migrate "+r.name)
	return nil
}
func (r *recorder) Start(context.Context) error {
	*r.order = append(*r.order, "start "+r.name)
	if r.failStart {
		return errBoom
	}
	return nil
}
func (r *recorder) Stop(context.Context) error {
	*r.order = append(*r.order, "stop "+r.name)
	return nil
}
