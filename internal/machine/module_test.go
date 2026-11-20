// SPDX-License-Identifier: MIT

package machine_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/core/coretest"
	"github.com/SDK-E/agentsone/internal/core/moduletest"
	humandomain "github.com/SDK-E/agentsone/internal/humanuser/domain"
	"github.com/SDK-E/agentsone/internal/machine"
	"github.com/SDK-E/agentsone/internal/machine/machinetest"
)

func TestModuleOwnsItsTags(t *testing.T) {
	deps, _ := coretest.NewDeps(t)
	m := machine.New(deps)
	if m.Name() != "machine" {
		t.Fatalf("name %q", m.Name())
	}
	moduletest.AssertOwnsTags(t, m, "machine", "net", "ide")
}

func TestNetworkRulesAndIDEAreNotHereYet(t *testing.T) {
	deps, _ := coretest.NewDeps(t)
	mux := moduletest.Mount(machine.New(deps))
	human := core.Caller{Kind: core.CallerHuman, Name: "alice", UID: -1}
	get := func(target string) int {
		req := httptest.NewRequest(http.MethodGet, target, nil)
		req = req.WithContext(core.WithCaller(req.Context(), human))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec.Code
	}
	for _, target := range []string{"/v1/net/agents", "/v1/ide"} {
		if code := get(target); code != http.StatusNotImplemented {
			t.Errorf("%s: status %d, want 501", target, code)
		}
	}
	if code := get("/v1/machine/pty"); code != http.StatusOK {
		t.Fatalf("terminals: status %d, want 200", code)
	}
}

func TestNoopSystemRefusesTerminalsInsteadOfStartingShells(t *testing.T) {
	deps, _ := coretest.NewDeps(t)
	mux := moduletest.Mount(machinetest.New(deps))
	human := core.Caller{Kind: core.CallerHuman, Name: "alice", UID: -1}
	req := httptest.NewRequest(http.MethodPost, "/v1/machine/pty", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(core.WithCaller(req.Context(), human))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("open on a machine with no terminals: %d %s", rec.Code, rec.Body)
	}
}

func TestNoopSystemOpensTheGateWithoutTouchingTheHost(t *testing.T) {
	deps, fakes := coretest.NewDeps(t)
	m := machinetest.New(deps)
	m.Provide(deps.Ports)
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := deps.Ports.Startup.Wait(context.Background()); err != nil {
		t.Fatalf("startup gate: %v", err)
	}
	if err := m.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	entries := fakes.Audit.Entries()
	if len(entries) == 0 || entries[len(entries)-1].Action != "machine.recipe.applied" {
		t.Fatalf("audit %+v", entries)
	}
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

type recordedAccounts struct {
	mu    sync.Mutex
	users []string
}

func (r *recordedAccounts) Ensure(_ context.Context, u humandomain.User) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.users = append(r.users, u.Name)
	return nil
}

func TestStartMakesEveryHumansLinuxUser(t *testing.T) {
	deps, fakes := coretest.NewDeps(t)
	fakes.Humans.Add("ada", 1000)
	fakes.Humans.Add("bob", 1001)
	sys := machinetest.NoopSystem()
	acc := &recordedAccounts{}
	sys.HumanAccounts = acc
	m := machine.NewWith(deps, sys)
	m.Provide(deps.Ports)
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := m.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	acc.mu.Lock()
	defer acc.mu.Unlock()
	if !slices.Equal(acc.users, []string{"h_ada", "h_bob"}) {
		t.Fatalf("users made at start %q", acc.users)
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
