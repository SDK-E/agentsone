// SPDX-License-Identifier: MIT

package agentstest

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/SDK-E/agentsone/internal/agents/domain"
	"github.com/SDK-E/agentsone/internal/core"
)

// Runtime is an in-memory runner supervisor. It tracks frozen scopes and
// refuses to wake frozen agents, like the real one.
type Runtime struct {
	// FailProvision, when set, is returned by Provision.
	FailProvision error

	mu           sync.Mutex
	provisioned  []string
	uids         map[string]int
	reconfigured []string
	retired      []string
	slept        []string
	frozen       map[string]bool
	wakes        []core.WakeReason
}

// NewRuntime returns a runtime with nothing provisioned.
func NewRuntime() *Runtime { return &Runtime{frozen: map[string]bool{}, uids: map[string]int{}} }

// Provision records a with the uid it was given and echoes that uid.
func (r *Runtime) Provision(_ context.Context, a core.Agent, _ *domain.Limits) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.FailProvision != nil {
		return 0, r.FailProvision
	}
	r.provisioned = append(r.provisioned, a.Name)
	r.uids[a.Name] = a.UID
	return a.UID, nil
}

// UID returns the uid name was last provisioned with.
func (r *Runtime) UID(name string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.uids[name]
}

// Reconfigure records a.
func (r *Runtime) Reconfigure(_ context.Context, a core.Agent, _ *domain.Limits) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reconfigured = append(r.reconfigured, a.Name)
	return nil
}

// Retire records name.
func (r *Runtime) Retire(_ context.Context, name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.retired = append(r.retired, name)
	return nil
}

// Wake refuses a frozen agent with core.ErrFrozen.
func (r *Runtime) Wake(_ context.Context, name string, reason core.WakeReason, _ json.RawMessage) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.frozen["agent:"+name] || r.frozen["all"] {
		return fmt.Errorf("wake %s: %w", name, core.ErrFrozen)
	}
	r.wakes = append(r.wakes, reason)
	return nil
}

// Sleep records name.
func (r *Runtime) Sleep(_ context.Context, name string, _ bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.slept = append(r.slept, name)
	return nil
}

// Freeze marks scope frozen. Only agent scopes refuse wakes here; the app
// keeps team and all freezes in the agents' state.
func (r *Runtime) Freeze(_ context.Context, scope core.FreezeScope) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.frozen[scope.String()] = true
	return nil
}

// Release clears scope's freeze.
func (r *Runtime) Release(_ context.Context, scope core.FreezeScope) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.frozen, scope.String())
	return nil
}

// Frozen reports whether scope is frozen.
func (r *Runtime) Frozen(scope string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.frozen[scope]
}

func (r *Runtime) list(s []string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), s...)
}

// Provisioned returns provisioned agent names in order.
func (r *Runtime) Provisioned() []string { return r.list(r.provisioned) }

// Reconfigured returns reconfigured agent names in order.
func (r *Runtime) Reconfigured() []string { return r.list(r.reconfigured) }

// Retired returns retired agent names in order.
func (r *Runtime) Retired() []string { return r.list(r.retired) }

// Slept returns agents put to sleep, in order.
func (r *Runtime) Slept() []string { return r.list(r.slept) }

// Wakes returns the reasons of delivered wakes, in order.
func (r *Runtime) Wakes() []core.WakeReason {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]core.WakeReason(nil), r.wakes...)
}
