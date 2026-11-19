// SPDX-License-Identifier: MIT

package machine_test

import (
	"context"
	"strings"
	"testing"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/core/coretest"
	"github.com/SDK-E/agentsone/internal/core/moduletest"
	"github.com/SDK-E/agentsone/internal/machine"
)

func TestModuleOwnsItsTags(t *testing.T) {
	deps, _ := coretest.NewDeps(t)
	m := machine.New(deps)
	if m.Name() != "machine" {
		t.Fatalf("name %q", m.Name())
	}
	moduletest.AssertOwnsTags(t, m, "machine", "net", "ide")
	// Terminals, network rules and the IDE still answer 501.
	moduletest.AssertEveryTagAnswersNotImplemented(t, m)
}

func TestStartReappliesTheRecipeAndNeverBlocks(t *testing.T) {
	deps, fakes := coretest.NewDeps(t)
	m := machine.New(deps)
	m.Provide(deps.Ports)
	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("start must not fail on a recipe problem: %v", err)
	}
	// The gate opens once the reapply ends, applied or failed.
	if err := deps.Ports.Startup.Wait(context.Background()); err != nil {
		t.Fatalf("startup gate: %v", err)
	}
	if err := m.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	var recorded bool
	for _, e := range fakes.Audit.Entries() {
		if strings.HasPrefix(e.Action, "machine.recipe.") {
			recorded = true
		}
	}
	if !recorded {
		t.Fatalf("no recipe outcome in audit: %+v", fakes.Audit.Entries())
	}
}

func TestUnknownSettingFailsStart(t *testing.T) {
	deps, _ := coretest.NewDeps(t)
	cfg, err := core.ParseConfig([]byte("[machine]\nreapply_timeout = \"1m\"\nbogus = 1\n"))
	if err != nil {
		t.Fatal(err)
	}
	deps.Config = &cfg
	m := machine.New(deps)
	if err := m.Start(context.Background()); err == nil || !strings.Contains(err.Error(), "machine settings") {
		t.Fatalf("start with an unknown key: %v", err)
	}
	if err := m.Stop(context.Background()); err != nil {
		t.Fatalf("stop after a failed start: %v", err)
	}
}
