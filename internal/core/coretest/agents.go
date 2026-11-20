// SPDX-License-Identifier: MIT

package coretest

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"

	"github.com/SDK-E/agentsone/internal/contract/contracttest"
	"github.com/SDK-E/agentsone/internal/core"
)

// Agents is an in-memory core.AgentDirectory. The chart comes from each
// agent's Manager field.
type Agents struct {
	mu     sync.Mutex
	agents map[string]core.Agent
}

var _ core.AgentDirectory = (*Agents)(nil)

// NewAgents returns an empty directory.
func NewAgents() *Agents { return &Agents{agents: map[string]core.Agent{}} }

// Add stores a, replacing any agent with the same name.
func (d *Agents) Add(a core.Agent) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.agents[a.Name] = a
}

// GetAgent returns the named agent.
func (d *Agents) GetAgent(_ context.Context, name string) (core.Agent, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.lookup(name)
}

// ListAgents returns every agent sorted by name.
func (d *Agents) ListAgents(context.Context) ([]core.Agent, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	all := make([]core.Agent, 0, len(d.agents))
	for _, a := range d.agents {
		all = append(all, a)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Name < all[j].Name })
	return all, nil
}

// Manager returns the agent's manager, or "" at the top of the chart.
func (d *Agents) Manager(_ context.Context, name string) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	a, err := d.lookup(name)
	return a.Manager, err
}

// IsBelow reports whether name is strictly below ancestor.
func (d *Agents) IsBelow(_ context.Context, name, ancestor string) (bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	chain, err := d.managers(name)
	if err != nil {
		return false, err
	}
	for _, m := range chain {
		if m == ancestor {
			return true, nil
		}
	}
	return false, nil
}

// TeamRoot returns the top of name's chart branch.
func (d *Agents) TeamRoot(_ context.Context, name string) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	chain, err := d.managers(name)
	if err != nil || len(chain) == 0 {
		return name, err
	}
	return chain[len(chain)-1], nil
}

// managers returns name's managers from nearest to the top.
func (d *Agents) managers(name string) ([]string, error) {
	a, err := d.lookup(name)
	if err != nil {
		return nil, err
	}
	var chain []string
	for a.Manager != "" && len(chain) <= len(d.agents) {
		chain = append(chain, a.Manager)
		if a, err = d.lookup(a.Manager); err != nil {
			return nil, err
		}
	}
	return chain, nil
}

func (d *Agents) lookup(name string) (core.Agent, error) {
	a, ok := d.agents[name]
	if !ok {
		return core.Agent{}, fmt.Errorf("agent %q: %w", name, core.ErrNotFound)
	}
	return a, nil
}

// Wake is one recorded Waker call.
type Wake struct {
	Agent   string
	Reason  core.WakeReason
	Payload json.RawMessage
}

// Waker records wakes instead of waking anyone.
type Waker struct {
	mu      sync.Mutex
	wakes   []Wake
	frozen  map[string]bool
	violate func(error) // set by NewStrictWaker
}

var _ core.Waker = (*Waker)(nil)

// NewWaker returns a Waker with no wakes.
func NewWaker() *Waker { return &Waker{frozen: map[string]bool{}} }

// NewStrictWaker returns a Waker that checks every payload against the
// runner contract, as the runner does. A payload that does not match is
// passed to onViolation (give it t.Error so the test fails even when the
// caller drops the wake's error), is not recorded, and makes Wake return
// core.ErrInvalid.
func NewStrictWaker(onViolation func(error)) *Waker {
	w := NewWaker()
	w.Strict(onViolation)
	return w
}

// Strict makes w check payloads like NewStrictWaker's.
func (w *Waker) Strict(onViolation func(error)) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.violate = onViolation
}

// Freeze makes later wakes of agent return core.ErrFrozen.
func (w *Waker) Freeze(agent string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.frozen[agent] = true
}

// Wake records the call, or returns core.ErrFrozen for a frozen agent.
func (w *Waker) Wake(_ context.Context, agent string, reason core.WakeReason, payload json.RawMessage) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.frozen[agent] {
		return fmt.Errorf("wake %q: %w", agent, core.ErrFrozen)
	}
	if w.violate != nil {
		if err := contracttest.ValidateWakePayload(string(reason), payload); err != nil {
			w.violate(fmt.Errorf("wake %q: %w", agent, err))
			return fmt.Errorf("wake %q: %w: %w", agent, core.ErrInvalid, err)
		}
	}
	w.wakes = append(w.wakes, Wake{Agent: agent, Reason: reason, Payload: payload})
	return nil
}

// Wakes returns the recorded wakes in order.
func (w *Waker) Wakes() []Wake {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]Wake(nil), w.wakes...)
}
