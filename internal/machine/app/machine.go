// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/machine/domain"
)

// jobQueueSize bounds queued package jobs; apt runs one at a time.
const jobQueueSize = 64

// Machine holds the machine control use cases.
type Machine struct {
	d Deps

	// recipeMu serialises recipe load-change-save and reapply.
	recipeMu sync.Mutex
	report   domain.Report

	jobsMu sync.Mutex
	jobs   map[string]domain.Job
	queue  chan string
}

// New builds the use cases on d.
func New(d Deps) *Machine {
	return &Machine{d: d, jobs: map[string]domain.Job{}, queue: make(chan string, jobQueueSize)}
}

// audit appends one fact. Detail is optional.
func (m *Machine) audit(ctx context.Context, a domain.Actor, action, target string, detail map[string]any) error {
	e := core.AuditEntry{Actor: a.Label(), Action: action, Target: target}
	if detail != nil {
		raw, err := json.Marshal(detail)
		if err != nil {
			return fmt.Errorf("audit %s detail: %w", action, err)
		}
		e.Detail = raw
	}
	if err := m.d.Audit.Append(ctx, e); err != nil {
		return fmt.Errorf("audit %s %s: %w", action, target, err)
	}
	return nil
}

// daemon is the actor for work the daemon does on its own.
var daemon = domain.Actor{Kind: domain.ActorSystem, Name: "agentsoned", UID: 0}

// changeRecipe loads the recipe, applies change and saves it.
func (m *Machine) changeRecipe(ctx context.Context, change func(*domain.Recipe)) error {
	m.recipeMu.Lock()
	defer m.recipeMu.Unlock()
	return m.changeRecipeLocked(ctx, change)
}

func (m *Machine) changeRecipeLocked(ctx context.Context, change func(*domain.Recipe)) error {
	r, _, err := m.d.Recipes.Load(ctx)
	if err != nil {
		return fmt.Errorf("load recipe: %w", err)
	}
	change(&r)
	if err := m.d.Recipes.Save(ctx, r); err != nil {
		return fmt.Errorf("save recipe: %w", err)
	}
	return nil
}
